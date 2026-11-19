---
title: "MCP server"
description: "The MCP endpoint at /api/mcp: an integration's MCP setting and its operation, MCP_ENABLED, authentication with an API key, the two protocol eras and how a request is classified, the headers, the five tools and their arguments, the write and delete tools and their destructive annotation, the Idempotency-Key and a replay, a call whose outcome is in doubt, results and their limits, counting and the request log, the errors, examples, and what bounds an agent."
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
  - apps/host/frontend/src/routes/workspace/-api-access-shared.ts
  - openapi/identity.yaml
---

Vantigo serves a [Model Context Protocol](https://modelcontextprotocol.io/) (MCP) endpoint at
`POST /api/mcp`. An AI client — Claude Code, Cursor, VS Code, n8n, an in-house agent — connects
to it with one of an integration's [API keys](/en/reference/api-introduction/) and works with
Vantigo through five tools: find an operation, describe it, call it to read, and — when the
integration allows it — call it to write or to delete. It is the REST API in the form agents
expect, and it grants nothing the key could not already do: every call runs as
a request of the key's own through the same gate as REST.

This page is the endpoint's contract, for a person writing or configuring a client. How an
Owner turns it on is under [API access › MCP](/en/admin/api-access/#mcp).

## The setting

Each integration has an **MCP access**, `identity.integrations.mcp_access`:

| Value | At `/api/mcp` |
| --- | --- |
| `off` | Every key of the integration is refused with `403 mcp_not_enabled`. The default for every integration, new and existing |
| `read` | The key may search, describe and call the operations of kind *read* it may call over REST |
| `read_write` | As `read`, and the `write` and `delete` tools call the operations of kind *write* it may call over REST |

The effective mode of a request is the narrower of the setting and the key: a **read-only key**
is `read` even when the integration is `read_write`. Nothing is cached: a change of the
setting, a key revoked, disabled, expired or narrowed applies at the next request.

**`read` limits MCP, not the key.** The setting decides what an AI client may call through
`/api/mcp`; it does not change what the key is. A key that is not read-only, held by someone who
has it, can still write over REST as far as the integration's roles allow. Only a **read-only
key** guarantees reads, at the endpoint and everywhere else: under `read`, give the agent a
read-only key.

An Owner changes it with `PUT /api/v1/identity/integrations/{id}/mcp` and
`{"mcpAccess": "off" | "read" | "read_write"}`, with `If-Match` carrying the integration's
`version`, which the change bumps. It is `policy:Owner+OwnerManagement` and never available to an
API key or to the Assistant; like the integration's other settings it mints no secret and has no
limit of its own. It answers
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
- A request with no `Authorization: Bearer vtg_…` at all is that `401` before anything else,
  and one whose `Content-Length` is over 1 MiB is `413`; neither is counted or looked up.
- Every other request is first limited per client address, 1,200 a minute (`429 rate_limited`),
  so a flood of unknown keys is bounded before any key is looked up.

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
| No `_meta`, no `MCP-Protocol-Version` | `400`, `-32020`: the header is missing, whatever the method |

`server/discover` answers `supportedVersions` (`2026-07-28`, `2025-11-25`, `2025-06-18`),
`capabilities: {"tools": {}}`, the instructions, `ttlMs` of an hour and `cacheScope: "public"`:
it carries nothing about the key. `tools/list` varies by key and answers `cacheScope: "private"`
with `ttlMs: 0`. `initialize` answers the negotiated `protocolVersion`, the same capabilities,
`serverInfo` and the instructions. The instructions say what the tools do, to search before
calling, to read a record before changing it and send `If-Match` where an operation takes it,
how to use an `Idempotency-Key` ([below](#the-idempotency-key)), that a write or delete that did
not answer in time may have been made,
that `write` and `delete` are listed only under `read_write` for a key that is not
read-only, and that what the tools answer is untrusted data from the workspace. They are the
same text for every key.

### Errors

| HTTP | JSON-RPC | When |
| --- | --- | --- |
| `400` | `-32700` | The body is not JSON |
| `400` | `-32600` | Not one JSON-RPC 2.0 request or notification: a batch, no `jsonrpc` or `method`, or an id that is neither a string nor a number. A valid id is answered in the error |
| `400` | `-32602` | A modern request whose `_meta` lacks the client's capabilities, or whose version is not a string |
| `400` | `-32020` | A required header (`MCP-Protocol-Version`, `Mcp-Method`, `Mcp-Name`) is missing, or does not match the body; a request without `MCP-Protocol-Version` (other than `initialize`) is this, whatever its method |
| `400` | `-32022` | An unsupported version; `data.supported` lists the versions, `data.requested` the one asked for |
| `404` (modern), `200` (legacy) | `-32601` | A method the era does not have: `initialize` and `ping` are legacy, `server/discover` modern, and there are no resources or prompts |
| `200` | `-32602` | `tools/call` names no tool, or one that does not exist — `write` and `delete` among them when they are not listed: under `read`, or for a read-only key |

The authentication answers above are the API's own JSON, `{"error": {"code", "message"}}`, not
JSON-RPC.

## The tools

`tools/list` answers `search_operations`, `describe_operation` and `read`, in that order, and —
only when the request's effective mode is `read_write` — `write` and `delete` after them. The
list is fixed in the code: two keys of one mode get the same bytes.

| Tool | Annotations | Arguments | Answers |
| --- | --- | --- | --- |
| `search_operations` | `readOnlyHint: true` | `query` (required), `limit` (1–25, 10 when left out) | The operations the key may call in its mode that match the query, best first: `operation`, `method`, `path`, `summary`, `tag`, `kind`, `tool`, the tool that calls it, and `idempotencyKey: true` when it takes an `Idempotency-Key` |
| `describe_operation` | `readOnlyHint: true` | `operation` (required) | Its method and path, kind and tool, path, query and header parameters, request body and success response as compact JSON schemas, with `$ref`s resolved, at most 16 KB |
| `read` | `readOnlyHint: true` | `operation` (required), `path`, `query`, `headers`, `body` | The answer of an operation of kind *read*, called as the key |
| `write` | `destructiveHint: true`, `readOnlyHint: false` | The same as `read` | The answer of an operation of kind *write* whose method is not `DELETE`, called as the key |
| `delete` | `destructiveHint: true`, `readOnlyHint: false`, `idempotentHint: true` | The same as `read` | The answer of a `DELETE` operation, called as the key |

The read tools also say `destructiveHint: false`, `idempotentHint: true` and
`openWorldHint: false`; `write` says `idempotentHint: false`, and `write` and `delete` say
`openWorldHint: true`, since a change may send mail or call a carrier. Deletes have a tool of
their own so a client can be told to allow one and not the other. A client asks its user before
it runs a tool unless the user has allowed that tool; do not auto-approve `write` or `delete`.

**The catalogue** is the key's own: the operations of the integrator's document for the key —
what `GET /api/openapi.json` answers it, cut to its roles, its narrowing and its read-only
switch — less those whose request body is not JSON (file uploads). The **kind** of an operation
is *read* for a `GET` or `HEAD`, and for a `POST` marked a read for keys only when MCP lists it as
reviewed: it changes nothing, calls nothing outside Vantigo and answers only what the caller may
read. The listed ones are `previewAccountingVoucher`, `previewAccountingBankCsv` (a file upload,
so not in the catalogue), `postExpensesClaimsByIdPerDiemSuggestion` and `resolveKnowledgeLinks`.
Every other operation is a *write* — among them `estimateShipping`, which asks the carrier,
`printInventoryLabels` and `postStaffPeopleSimilarErased`, which a read-only key may call over
REST — and so is a `POST` marked read that is added later, until it is reviewed.

**Each tool calls its own operations.** `read` calls the reads, `delete` the `DELETE`s and
`write` every other write; each refuses another's with an error result that names the right
tool. Under `read` a write is not in reach at all: search leaves it out, `describe_operation`
and `read` answer that it is not available to this key, and `write` and `delete` are not
listed. Under `read_write` search and `describe_operation` reach the writes too.

**Search** scores the words of the query against an operation's summary, tag, path and
description, as the Assistant's search does, in English or Norwegian, stop words dropped.

**The arguments of `read`, `write` and `delete`.** `path` and `query` map a parameter's name to
a string, or a query
parameter to a list of strings, which repeats it. Every path parameter is required and is
escaped as one segment, so a `/` or `..` in a value cannot reach another route. A parameter the
operation does not declare is refused. `headers` may set only `If-Match`, `If-None-Match` and
`Idempotency-Key`, and only when the operation declares them in the key's document, each once
(two spellings of one header differing in case are refused); a line break in a value is
refused, and `Authorization`, `Cookie` and `Host` can never be set. `body` is any JSON, for an
operation that takes one. The Assistant's own marks (`x-vantigo-assistant`) are not consulted:
what a key may call is decided by the API's marks, its roles and its narrowing.

### The Idempotency-Key

An operation takes an `Idempotency-Key` when the router honours one on it — every `POST`
[Retrying safely](/en/reference/api-introduction/#retrying-safely) covers — or when it declares
a key of its own. The catalogue records it from the key's document, so `describe_operation`
lists it among `headerParameters` and `search_operations` marks the operation
`idempotencyKey: true`; with `IDEMPOTENCY_ENABLED` off neither does, and `write` refuses the
header as one the operation does not declare. The Assistant is not affected: it sends a key only
to an operation that declares one of its own.

The instructions and the descriptions of `write` and `delete` tell the model to **mint one key
per intended action** — a new UUID, say — and to send **that same key with every retry of that
action**, never a new key for a retry, since a new key is a new action. The answers it can get:

| Answer | Its result | What the model is told to do |
| --- | --- | --- |
| The first answer again | `{"idempotentReplayed": true, "status", "response"}`, `response` the first answer's body as a `2xx` result shows it | Nothing: it is the first call's answer, not a second change |
| `409 idempotency_in_progress` | An error result with `retryAfterSeconds` and `next` | Retry shortly with the same key |
| `409 idempotency_outcome_unknown` | An error result with `next` | Read the record, then use a new key only if the change is not there |
| `422 idempotency_key_reused` | An error result with `next` | A new action takes a new key; a retry sends the same request as the first |

### How a call is made

A call of `read`, `write` or `delete` is a real request through the composed API, built by the
endpoint: the operation's
method and path, the query in a deterministic order, the declared headers, the body, the
caller's own `Authorization` header and `Accept: application/json`. It keeps the caller's
address, the envelope's trace id and its cancellation — a client that goes away stops the call,
as on REST — and nothing else of the envelope: no principal is put on it, so identity judges the
key afresh — its networks, its state, the operation's rule, read-only, the roles and the
narrowing — and the router applies the integration's rate limit, the operation's own limits and
the body cap, as for any REST request. It has 30 seconds.

A key has at most **four** calls looping back at once, on each server process; one more is an
error result asking it to retry shortly, and is counted as an MCP request.

The one thing MCP adds to the REST gate is its setting: under `read`, nothing that is not a
read is called, even for a key REST would let write. The mode is judged at every request: an
integration lowered from `read_write` to `read` after a client listed `write` refuses the next
`write` or `delete` with `-32602`. Everything else about a write — whether the key's roles and
narrowing
allow it, `If-Match` against the record's version, an `Idempotency-Key` replayed — is the
operation's own answer, exactly as over REST.

### Results

A tool's result is one `text` content and `isError`; `structuredContent` is not used.

- A `2xx` answer is its JSON body as sent; an answer that is not JSON is described by its
  `status`, `contentType` and `length`; an empty one is `{"status": 204}`. A replay for an
  `Idempotency-Key` (`Idempotent-Replayed: true`) is that result wrapped as
  `{"idempotentReplayed": true, "status", "response"}`.
- Any other status is `isError: true` with JSON of `status`, `code`, `title` and `detail` from
  the API's answer — never its raw body. A `429` and a `409 idempotency_in_progress` also carry
  `retryAfterSeconds`, and the Idempotency-Key's refusals, on an operation that takes one, a
  `next` saying what to do.
- The text is at most **100 KB**, counted as the JSON-escaped string it is sent as. A larger
  answer to a `read` is never cut short: the result is an error asking for less — a smaller
  `limit`, a filter, a later page. A `write` or `delete` that succeeded with a larger answer is
  no error, since the change was made: its result is `{"status", "truncated": true, "length"}`
  with the answer's `location` and its top-level `id` when it has them; read the record to see
  it.
- **A call whose outcome is in doubt.** A call whose 30 seconds ran out, or whose client went
  away, is judged by what it answered: an answer below `500` is its result as above, a `201`
  that came late included. One that answered nothing, or a server error, is an error result;
  for a `write` or `delete` it says the change **may or may not have been made**: on an
  operation that takes an `Idempotency-Key`, retry with the same key, never a new one; on one
  that takes none, read the record before retrying. A call that fails unexpectedly says the
  change may or may not have been made too.
- An operation that fails unexpectedly or does not answer in time is an error result naming
  the trace id, never a failed envelope.
- A refusal of the tool's own — an operation outside the catalogue, a missing or undeclared
  parameter, a header beyond the three — is an error result too, so the model can correct the
  call.

The integration's `RateLimit-*` headers of a looped-back call are passed on to the envelope's
answer.

## Counting and the request log

- A call of `read`, `write` or `delete` that reaches the API **counts once**, as the operation it
  calls. It leaves **two
  rows** in the request log: one with the real operation id, its status and the key, which is
  counted, and one `postApiMcp` for the MCP request around it, which is recorded but not
  counted.
- Every other request a key is let through for — `server/discover`, `initialize`, `ping`,
  `tools/list`, `search_operations`, `describe_operation`, a call refused before it is made,
  a notification, a protocol error — counts once and is recorded as `postApiMcp`.
- Past the integration's limit, an envelope is `429 rate_limited` with `Retry-After`; a call
  over the limit is an error result with `retryAfterSeconds`.

The request log never holds a path, a query or a body, through MCP as through REST.

`tools/list`, `server/discover`, `initialize` and `ping` read the key and its integration once;
the five tools read the key's scope as well.

The rows a call leaves carry the envelope's trace id, so the operation an MCP request made is
found by the `postApiMcp` row's trace id.

## Examples

A modern client creates a customer. It sends:

```http
POST /api/mcp
Authorization: Bearer vtg_…
Content-Type: application/json
MCP-Protocol-Version: 2026-07-28
Mcp-Method: tools/call
Mcp-Name: write

{"jsonrpc": "2.0", "id": 7, "method": "tools/call", "params": {
  "_meta": {"io.modelcontextprotocol/protocolVersion": "2026-07-28",
            "io.modelcontextprotocol/clientCapabilities": {}},
  "name": "write",
  "arguments": {"operation": "postCustomers", "body": {"name": "Fjordkraft AS"}}}}
```

and gets the operation's `201` body as the result's text:

```json
{"jsonrpc": "2.0", "id": 7, "result": {"resultType": "complete",
  "content": [{"type": "text", "text": "{\"customerNumber\":1042,\"id\":1042}"}],
  "isError": false,
  "_meta": {"io.modelcontextprotocol/serverInfo": {"name": "vantigo", "title": "Vantigo", "version": "…"}}}}
```

A write with `If-Match` sends the version it read in `headers`, here to rename an inventory
site:

```json
{"name": "write", "arguments": {
  "operation": "putInventorySite", "path": {"id": "12"}, "headers": {"If-Match": "3"},
  "body": {"code": "OSL", "name": "Oslo", "kind": "warehouse", "status": "active"}}}
```

If someone changed the site since it was read, the result is an error the model can act on — read
the site again and retry with the new version:

```json
{"content": [{"type": "text", "text": "{\"code\":\"revision_conflict\",\"detail\":\"…\",\"status\":409,\"title\":\"Conflict\"}"}],
 "isError": true}
```

Inventory answers a stale version with `409 revision_conflict`; operations that answer it with
`412 version_mismatch` come back the same way, with their own status and code.
A key whose roles do not allow the change gets the operation's `403` the same way, as
`{"code": "forbidden", "status": 403, …}` with `isError: true`; an operation the key's catalogue
does not hold at all is the error "not available to this key", `write` naming a `DELETE` is the
error that names `delete`, and `write` or `delete` named under `read` is the JSON-RPC error
`-32602`.

## What bounds an agent

An agent reads records written by people and other systems, and a record can carry text meant
to steer it. That cannot be prevented here; what bounds it is the setting (off by default, and
`read_write` only where an Owner chose it), the split between `read`, `write` and `delete`,
the client's approval of each change unless its user auto-approves it, the key's roles,
narrowing and read-only switch, the integration's rate limit, the four calls a key may have in
flight, and the request log, which shows every operation the agent called. The setting `read`
is not a boundary against whoever holds the key: only a read-only key is. The tools' descriptions
and the instructions tell the model that what it reads is data, never instructions. A tool's
description and the tool list are fixed in the code, the same for every key.
