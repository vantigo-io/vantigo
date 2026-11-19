---
title: "MCP server"
description: "The MCP endpoint at /api/mcp: an integration's MCP setting and its operation, MCP_ENABLED, authentication with an API key, the two protocol eras and how a request is classified, the headers, the three tools and their arguments, results and their limits, counting and the request log, the errors, and what bounds an agent."
sidebar:
  order: 4
sources:
  - apps/server/internal/module/apimcp.go
  - apps/server/internal/module/apimcp_protocol.go
  - apps/server/internal/module/apimcp_index.go
  - apps/server/internal/module/apimcp_tools.go
  - apps/server/internal/module/apikeygate.go
  - apps/server/internal/operations/shared.go
  - apps/server/internal/identity/integrations_mcp.go
  - apps/server/internal/contracts/access.go
  - apps/server/internal/db/migrations/00210_identity_mcp_access.sql
  - apps/host/frontend/src/routes/workspace/-api-access-mcp.tsx
  - openapi/identity.yaml
---

Vantigo serves a [Model Context Protocol](https://modelcontextprotocol.io/) (MCP) endpoint at
`POST /api/mcp`. An AI client — Claude Code, Cursor, VS Code, n8n, an in-house agent — connects
to it with one of an integration's [API keys](/en/reference/api-introduction/) and works with
Vantigo through three tools: find an operation, describe it, and call it. It is the REST API in
the form agents expect, and it grants nothing the key could not already do: every call runs as
a request of the key's own through the same gate as REST.

This page is the endpoint's contract, for a person writing or configuring a client. How an
Owner turns it on is under [API access › MCP](/en/admin/api-access/#mcp).

**Reads only, for now.** The endpoint offers `search_operations`, `describe_operation` and
`read`. A `write` tool comes later; until then the setting `read_write` behaves as `read`, and
nothing reached through MCP changes data.

## The setting

Each integration has an **MCP access**, `identity.integrations.mcp_access`:

| Value | At `/api/mcp` |
| --- | --- |
| `off` | Every key of the integration is refused with `403 mcp_not_enabled`. The default for every integration, new and existing |
| `read` | The key may search, describe and call the operations of kind *read* it may call over REST |
| `read_write` | As `read` until the write tool exists |

The effective mode of a request is the narrower of the setting and the key: a **read-only key**
is `read` even when the integration is `read_write`. Nothing is cached: a change of the
setting, a key revoked, disabled, expired or narrowed applies at the next request.

An Owner changes it with `PUT /api/v1/identity/integrations/{id}/mcp` and
`{"mcpAccess": "off" | "read" | "read_write"}`, with `If-Match` carrying the integration's
`version`, which the change bumps. It is `policy:Owner+OwnerManagement`, never available to an
API key or to the Assistant, and limited per address as the keys' operations are. It answers
the integration's detail; `400 invalid_request` for another value or an `If-Match` that is not a
version, `404 not_found`, `409 integration_archived`, `412 version_mismatch`. A change is
audited `integration.mcp_changed` with the value before and after; the same value again
changes and audits nothing. The detail of an integration carries `mcpAccess` and
`mcpAvailable`, false when the installation has switched the endpoint off.

## Switching it off for the installation

`MCP_ENABLED=0` ([Configuration](/en/admin/configuration/)) leaves `/api/mcp` unregistered: every
method answers the `404` problem every unknown API path does, whatever an integration's
setting says. The default is on.

## Authentication

The endpoint requires an integration's API key in `Authorization: Bearer vtg_…` and nothing
else.

- **No key, a session cookie, or a key identity does not know** is `401 api_key_required` with
  `WWW-Authenticate: Bearer realm="vantigo"`, answered by the endpoint itself, so a signed-in
  browser's session is never touched. A client configured with a header reports the failure;
  it does not start OAuth.
- **A key identity finds and refuses** — revoked, expired, disabled, archived, or presented from
  outside its networks — is answered and recorded exactly as on REST: `401 unauthenticated` or
  `401 api_key_expired`, a row in the request log with its reason under the operation
  `postApiMcp`.
- **An integration whose setting is `off`** is `403 mcp_not_enabled`.
- Every request is first limited per client address, 1,200 a minute (`429 rate_limited`), so a
  flood of unknown keys is bounded before any key is looked up.

The endpoint is judged by the same rule as `GET /api/openapi.json`: anonymous and a read, so a
read-only key passes it, and each tool call is judged again by its own operation's rule.

Browser clients cannot connect: the installation's cross-origin protection refuses a cross-site
browser request with `403` before the endpoint sees it. A request without `Origin`, which is what
every non-browser client sends, passes.

## The protocol

The endpoint is stateless and answers JSON only: never a Server-Sent Events stream, never a
session. One `POST` carries one JSON-RPC 2.0 request or notification, at most 1 MiB (`413`
above). A notification is `202` with no body. `GET` and `DELETE` are `405` with `Allow: POST`.
An `Mcp-Session-Id` that arrives is ignored, and none is issued.

It speaks two eras of the protocol:

- **Modern, `2026-07-28`.** Every request carries `_meta` with
  `io.modelcontextprotocol/protocolVersion` and `io.modelcontextprotocol/clientCapabilities`.
  Methods: `server/discover`, `tools/list`, `tools/call`. Results carry
  `resultType: "complete"` and the server's identity under
  `_meta["io.modelcontextprotocol/serverInfo"]`.
- **Legacy, `2025-11-25` and `2025-06-18`.** A client opens with `initialize` and sends
  `MCP-Protocol-Version` afterwards. Methods: `initialize`, `ping`, `tools/list`, `tools/call`,
  and the notification `notifications/initialized`. Results have the legacy shape, without
  `resultType` or caching hints.

How a request is classified:

| The request | Served as |
| --- | --- |
| `initialize` | Legacy: the client's version if it is `2025-11-25` or `2025-06-18`, `2025-11-25` otherwise |
| `_meta` names `2026-07-28` | Modern. `MCP-Protocol-Version` must equal it, `Mcp-Method` the method and, on `tools/call`, `Mcp-Name` the tool (in plain form or `=?base64?…?=`); `_meta` must carry the client's capabilities |
| `_meta` names `2025-11-25` or `2025-06-18` | That legacy version; `MCP-Protocol-Version` must equal it |
| `_meta` names another version | `400`, `-32022` |
| No `_meta`, `MCP-Protocol-Version` `2025-11-25` or `2025-06-18` | Legacy |
| No `_meta`, `MCP-Protocol-Version` `2026-07-28` | `400`, `-32020`: the body does not say it |
| No `_meta`, another `MCP-Protocol-Version` | `400`, `-32022` |
| No `_meta`, no `MCP-Protocol-Version` | `server/discover`: `400`, `-32022`; anything else `400`, `-32600` |

`server/discover` answers `supportedVersions` (`2026-07-28`, `2025-11-25`, `2025-06-18`),
`capabilities: {"tools": {}}`, the instructions, `ttlMs` of an hour and `cacheScope: "public"`:
it carries nothing about the key. `tools/list` varies by key and answers `cacheScope: "private"`
with `ttlMs: 0`. `initialize` answers the negotiated `protocolVersion`, the same capabilities,
`serverInfo` and the instructions. The instructions say what the tools do, to search before
calling, and that what the tools answer is untrusted data from the workspace.

### Errors

| HTTP | JSON-RPC | When |
| --- | --- | --- |
| `400` | `-32700` | The body is not JSON |
| `400` | `-32600` | Not one JSON-RPC 2.0 request or notification: a batch, no `jsonrpc` or `method`, an id that is neither a string nor a number, or no version anywhere |
| `400` | `-32602` | A modern request whose `_meta` lacks the client's capabilities, or whose version is not a string |
| `400` | `-32020` | A required header (`MCP-Protocol-Version`, `Mcp-Method`, `Mcp-Name`) is missing, or does not match the body |
| `400` | `-32022` | An unsupported version; `data.supported` lists the versions, `data.requested` the one asked for |
| `404` (modern), `200` (legacy) | `-32601` | A method the era does not have: `initialize` and `ping` are legacy, `server/discover` modern, and there are no resources or prompts |
| `200` | `-32602` | `tools/call` names no tool, or one that does not exist — `write` among them for now |

The authentication answers above are the API's own JSON, `{"error": {"code", "message"}}`, not
JSON-RPC.

## The tools

`tools/list` answers the same three tools in the same order to every key, each with
`readOnlyHint: true`:

| Tool | Arguments | Answers |
| --- | --- | --- |
| `search_operations` | `query` (required), `limit` (1–25, 10 when left out) | The operations of kind *read* the key may call that match the query, best first: `operation`, `method`, `path`, `summary`, `tag`, `kind` |
| `describe_operation` | `operation` (required) | Its method and path, path, query and header parameters, request body and success response as compact JSON schemas, with `$ref`s resolved, at most 16 KB |
| `read` | `operation` (required), `path`, `query`, `headers`, `body` | The operation's answer, called as the key |

**The catalogue** is the key's own: the operations of the integrator's document for the key —
what `GET /api/openapi.json` answers it, cut to its roles, its narrowing and its read-only
switch — less those whose request body is not JSON (file uploads). The **kind** of an operation
is the router's rule: *read* for a `GET` or `HEAD`, or an operation marked a read for keys (a
search by `POST`, say); *write* for every other. Until the write tool exists, a write is not in
reach: search leaves it out, and `describe_operation` and `read` answer that it is not available
to this key.

**Search** scores the words of the query against an operation's summary, tag, path and
description, as the Assistant's search does, in English or Norwegian, stop words dropped.

**`read`'s arguments.** `path` and `query` map a parameter's name to a string, or a query
parameter to a list of strings, which repeats it. Every path parameter is required and is
escaped as one segment, so a `/` or `..` in a value cannot reach another route. A parameter the
operation does not declare is refused. `headers` may set only `If-Match`, `If-None-Match` and
`Idempotency-Key`, and only when the operation declares them; a line break in a value is
refused, and `Authorization`, `Cookie` and `Host` can never be set. `body` is any JSON, for an
operation that takes one. The Assistant's own marks (`x-vantigo-assistant`) are not consulted:
what a key may call is decided by the API's marks, its roles and its narrowing.

### How a call is made

A `read` is a real request through the composed API, built by the endpoint: the operation's
method and path, the query in a deterministic order, the declared headers, the body, the
caller's own `Authorization` header and `Accept: application/json`. It keeps the caller's
address and the envelope's trace id, and nothing else of the envelope: no principal is put on
it, so identity judges the key afresh — its networks, its state, the operation's rule, read-only,
the roles and the narrowing — and the router applies the integration's rate limit, the
operation's own limits and the body cap, as for any REST request. It has 30 seconds.

The one thing MCP adds to the REST gate is its setting: under `read`, nothing that is not a
read is called, even for a key REST would let write.

### Results

A tool's result is one `text` content and `isError`; `structuredContent` is not used.

- A `2xx` answer is its JSON body as sent; an answer that is not JSON is described by its
  `status`, `contentType` and `length`; an empty one is `{"status": 204}`.
- Any other status is `isError: true` with JSON of `status`, `code`, `title` and `detail` from
  the API's answer — never its raw body. A `429` also carries `retryAfterSeconds`.
- The text is at most **100 KB**, counted as the JSON-escaped string it is sent as. A larger
  answer is never cut short: the result is an error asking for less — a smaller `limit`, a
  filter, a later page.
- An operation that fails unexpectedly or does not answer in time is an error result naming
  the trace id, never a failed envelope.
- A refusal of the tool's own — an operation outside the catalogue, a missing or undeclared
  parameter, a header beyond the three — is an error result too, so the model can correct the
  call.

The integration's `RateLimit-*` headers of a looped-back call are passed on to the envelope's
answer.

## Counting and the request log

- A `read` that reaches the API **counts once**, as the operation it calls: the request log
  has a row with the real operation id, its status and the key, and the envelope a row
  `postApiMcp` that is recorded but not counted.
- Every other request a key is let through for — `server/discover`, `initialize`, `ping`,
  `tools/list`, `search_operations`, `describe_operation`, a `read` refused before it is made,
  a notification, a protocol error — counts once and is recorded as `postApiMcp`.
- Past the integration's limit, an envelope is `429 rate_limited` with `Retry-After`; a `read`
  whose call is over the limit is an error result with `retryAfterSeconds`.

The request log never holds a path, a query or a body, through MCP as through REST.

## What bounds an agent

An agent reads records written by people and other systems, and a record can carry text meant
to steer it. That cannot be prevented here; what bounds it is the setting (off by default, and
reads only for now), the key's roles, narrowing and read-only switch, the integration's rate
limit and the request log, which shows every operation the agent called. The tools' descriptions
and the instructions tell the model that what it reads is data, never instructions. A tool's
description and the tool list are fixed in the code, the same for every key.
