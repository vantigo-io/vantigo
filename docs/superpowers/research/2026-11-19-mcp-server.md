# An MCP server at /api/mcp — research

Research, 2026-11-19, for API access §12 ("an MCP server at `/api/mcp`") after the webhooks
phase (`specs/2026-11-18-outgoing-webhooks-design.md`). Code references are against
`988079aaf`; `srv/` is `apps/server/internal/`. Claims about the protocol and other
products come from public documentation fetched 2026-11-19; "(unverified)" means the
source did not confirm it, and the implementer must read the cited spec page before
relying on it.

## 1. What an MCP server is for here

The Model Context Protocol lets an AI client (Claude Code, Cursor, VS Code, ChatGPT, n8n,
in-house agents) discover and call tools on a remote server over HTTP. For Vantigo that
means: an **integration's API key** (K8–K11) lets an agent work with Vantigo under the
integration's roles — the same reach as the REST API, in the form agents expect. It is
not a new capability (every call is already possible with the key); it removes the glue.

## 2. The protocol today

The latest revision is **2026-07-28**
(<https://modelcontextprotocol.io/specification/2026-07-28/basic/versioning.md>); earlier
ones are 2025-11-25, 2025-06-18, 2025-03-26 and 2024-11-05. It changed shape:

- **No `initialize` handshake, no protocol sessions, no `Mcp-Session-Id`, no GET stream,
  no `ping`.** Every request carries `_meta` with `io.modelcontextprotocol/protocolVersion`,
  `clientInfo` and `clientCapabilities`. A server must answer a new `server/discover` RPC.
- Transport (Streamable HTTP, one endpoint): each JSON-RPC request or notification is its own
  `POST`; the client sends `Accept: application/json, text/event-stream`; a notification
  gets `202` and no body; a request gets `application/json` or a request-scoped SSE
  stream, so a plain JSON reply is always allowed. Headers `MCP-Protocol-Version`,
  `Mcp-Method` and, for `tools/call`, `Mcp-Name` must match the body (else `400`,
  `-32020`); an unsupported version is `400`/`-32022` with `data.supported`; an unknown
  method `404`/`-32601`. `GET`/`DELETE` are `405`. Servers must validate `Origin`.
- **Dual era.** Legacy clients (2025-11-25 and earlier) still exist; they `initialize`.
  One endpoint can serve both: a request carrying `_meta` is modern and stateless, an
  `initialize` selects legacy semantics.
- Tools: `tools/list` is paginated and may vary with the request's authorization (never per
  connection); `inputSchema` is JSON Schema 2020-12 and must be an object; tool names are
  1–128 characters of `[A-Za-z0-9_.-]`; `annotations` (`readOnlyHint`, `destructiveHint`,
  `idempotentHint`, `openWorldHint`) are untrusted hints. **Protocol errors are JSON-RPC
  errors; execution errors (a 403 from the API, a validation failure) are a normal result
  with `isError: true`** so the model can correct itself. Auth errors are HTTP `401`/`403`.
  A server MUST validate input, enforce access control and rate-limit.
- Tool-count limits are real: VS Code is reported to cap near 128 tools per request
  (unverified); every tool costs the client's context. Cloudflare and Stripe expose
  thousands of endpoints through 2–4 generic tools (`search`, `execute`; `stripe_api_search`,
  `_details`, `_read`, `_write`) for that reason (vendor blogs).
- ChatGPT's developer mode treats a tool **without `readOnlyHint`** as a write and asks
  confirmation each time (OpenAI developer-mode docs).

## 3. Authorization

- The MCP authorization spec is OAuth 2.1 with RFC 9728 protected-resource metadata and
  client-ID metadata documents (dynamic client registration is deprecated). It is
  **optional** for HTTP servers and says nothing of static keys.
- In practice **static bearer keys work** in Claude Code
  (`claude mcp add --transport http NAME URL --header "Authorization: Bearer …"`),
  Cursor, VS Code/Copilot and n8n. **ChatGPT's connectors need OAuth** (no header option
  documented). Claude.ai custom connectors: sources conflict (unverified).
- Claude Code, when an `Authorization` header is configured, does **not** start OAuth on a
  `401`: it reports the connection failed. So a bad key must give a clean `401` with a
  clear message. Stripe and Linear accept an API key as a Bearer header beside OAuth.
- Consequence: this phase serves key-based clients. OAuth (API access §12 "OAuth apps") is
  what unlocks ChatGPT; the endpoint is built so it can be added without a redesign (a
  `WWW-Authenticate` challenge already exists, `access_apikey.go:197`).

## 4. Library or hand-rolled

- The official Go SDK `github.com/modelcontextprotocol/go-sdk` supports 2026-07-28 from
  v1.7.0 (`Stateless: true` is required for modern requests) and keeps legacy
  compatibility; v1.8.0 hardened parsing; v1.9.0 is a pre-release; behaviour flags
  (`MCPGODEBUG`) and slipping removal dates show churn. Origin-protection options and the
  raw-schema `AddTool` signature were not confirmed.
- The surface Vantigo needs is small and fixed: `server/discover`, legacy `initialize` +
  `notifications/initialized` + `ping`, `tools/list`, `tools/call`, JSON responses, the
  error codes above. That is a few hundred lines of Go with **no dependency**, a fixed and
  testable contract, and no churn. A hand-rolled core also composes with Vantigo's own
  authentication, rate limit and request log, which an SDK's auth hook would have to be
  bent around.
- *Recommendation:* hand-roll, with a conformance test file that pins each message shape to
  the spec pages named in the design; keep the SDK as the fallback if the spec grows.

## 5. What our code already gives

- **The dispatcher** (`srv/operations`, `operations.go`): enumerates every operation of the
  merged contract; `Search` (token match, 50 results), `Describe` (compact JSON of
  parameters/body/response, 8 KB), `Call` (builds a request and runs the **composed API
  handler in-process** through a recorder capped at 1 MiB). It is used for the
  Assistant's grant and the agents'; both are bound to a **person's grant**, not a key
  (`identity.Check` sees a grant before a key, `access.go:134-170`).
- **A key's whole gate** is in the router (`Router.wrap`, `router.go:353-400`): per-operation
  limit, `Access.Check` (key parse, network allowlist by `httpx.ClientIP`, revoked/disabled/
  expired, `x-vantigo-api` marks, read-only, role evaluation with `APINever` and the key's
  narrowing), `keyGate.serve` (the integration's rate limit, `RateLimit-*` headers, the
  request-log row with operation id and key id), body cap, handler.
- **The scoped integrator document** (`/api/openapi.json`, `apidocument.go`,
  `integrator.Build`): exactly the operations a key can call now, filtered by permissions
  and read-only. It is the natural source for a tool list, but it strips `x-vantigo-*`,
  so the Assistant marks need the unstripped contract (`apiDocuments.contract()`).
- **Numbers** (`srv/openapi/specs`): 1,418 operations; about 1,050 reachable by a key at most
  (about 490 reads, 560 writes). One tool per operation is impossible (§2).
- **Streaming/HTTP stack**: no write timeout (`main.go:99-104`), SSE precedent in
  `assistant/sse.go`; `/api/mcp` registers beside `GET /api/openapi.json` in `compose.go`
  (before the `/api/` catch-all); CSRF protection (`http.CrossOriginProtection`) admits
  server-side clients and rejects cross-site browser requests; there is no CORS.

**Dispatch design that follows.** A tool call must pass *the same gate* as a REST call. The
Assistant's in-process `Call` cannot be reused as is (grants beat keys; it strips
`Authorization`; its `RemoteAddr` is `kind-uuid`, which would defeat a key's network
allowlist). The clean way: the MCP handler builds the request **with the caller's own
`Authorization: Bearer vtg_…` and the caller's resolved client IP** and runs it through the
composed API handler. Every rule above then applies by construction, and the request log
gets the real operation id and key id. The tool layer adds no authorization of its own.

## 6. The tool surface

A key must see only what it can call, in few tools. Recommended four:

| Tool | Does | Annotations |
| --- | --- | --- |
| `search_operations` | Token search over the key's callable operations (summary, tag, path), at most 25, with id, method, path, summary, whether it writes | `readOnlyHint` |
| `describe_operation` | One operation's parameters, request body and response as a compact schema | `readOnlyHint` |
| `read` | Runs a **read** operation (`GET`, `HEAD` or `x-vantigo-api: read`) | `readOnlyHint` |
| `write` | Runs any other operation the key may call; **absent for a read-only key** | `destructiveHint` |

Splitting `read` from `write` is what lets clients auto-approve reads (and lets ChatGPT's
rule work) while writes keep the client's confirmation. Tool results are JSON text with
`isError` for failures; the status, problem code and the API's own message are passed
through. A result over 100 KB is refused with a message to narrow the request (the API's
own paging, `limit`, filters), not silently truncated. All results are treated as untrusted
data by the model; the descriptions say so.

## 7. Risks

- **Prompt injection through data.** A record's free text can instruct an agent. The agent
  holds a key, so a hijacked agent acts with the key's roles. Mitigations that are ours:
  per-integration opt-in and a read-only mode (§8 Q1), the `read`/`write` split,
  narrowing and roles as today, rate limit, the request log. We cannot make the model safe.
- **Tool poisoning / rug pull.** Our descriptions are ours and fixed in code; operation
  summaries come from the contract (reviewed in PRs). The tool list is deterministic.
- **Confused deputy.** None: the call runs as the key, through the key's gate. Nothing is
  done with the server's own authority.
- **Cost / DoS.** The integration's existing per-minute limit applies per call; the envelope
  must not double-count (§8 Q8). Responses are capped; no long-lived streams.
- **Confirmations.** The Assistant's `confirm` mode relies on a human card; a key has no
  human. The MCP client's own approval, driven by `destructiveHint`, is the human step.
- **The Assistant's `never` marks (186 operations)** exist for what a *person's* Assistant may
  do; 28 are Knowledge operations. They do not describe an integration's risk, which
  `x-vantigo-api` and roles do already (K53).
- **Browser clients** cannot connect (no CORS, CSRF refuses cross-site); that is intended.

## 8. Questions for the owner, with recommended defaults

1. **Opt-in per integration?** *Default:* yes. A new setting on the integration, `mcp_access`
   = `off` | `read` | `read_write`, **default `off`** for every integration (existing keys
   were issued for REST), changed by an Owner and audited. A key on an integration with
   `off` is refused at `/api/mcp` with `403 mcp_not_enabled`.
2. **Tool surface.** *Default:* the four generic tools of §6; no per-operation tools.
3. **Do the Assistant's `x-vantigo-assistant` marks limit a key?** *Default:* no. Keys follow
   `x-vantigo-api`, their roles and narrowing; the Assistant marks concern the Assistant.
4. **Protocol versions.** *Default:* 2026-07-28 modern and the legacy `initialize` flow for
   2025-11-25/2025-06-18, stateless, JSON responses only, no SSE, no GET stream.
5. **Auth.** *Default:* a static bearer key only; OAuth/ChatGPT is a later phase with
   "OAuth apps". A clean `401` with `WWW-Authenticate: Bearer realm="vantigo"` for a bad
   key.
6. **Resources and prompts.** *Default:* none in this phase. Knowledge articles as
   `knowledge://article/{id}` resources are a candidate for a later phase; the `read` tool
   already reaches them.
7. **Result size.** *Default:* refuse above 100 KB with guidance.
8. **Rate limit.** *Default:* a tool call counts once, by its looped-back REST request;
   `server/discover`, `tools/list` and `initialize` count nothing but are logged under one
   operation id (`postApiMcp`); an unauthenticated flood is bounded by the existing
   key-failure handling.
9. **Workspace kill switch.** *Default:* `MCP_ENABLED` (default true), as the other
   features; when false `/api/mcp` is a 404.
10. **Where Owners connect it.** *Default:* the MCP mode on the integration's Overview and a
    connection snippet (URL, header, `claude mcp add` line) next to the key's curl hint.
