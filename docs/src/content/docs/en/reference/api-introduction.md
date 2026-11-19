---
title: "API introduction"
description: "Calling Vantigo's API as an integration: authenticating with an API key, the document of what a key reaches, the error codes a refusal carries, the rate limit and its headers, the interactive reference at /api, connecting an AI client over MCP, and the compatibility promise."
sidebar:
  order: 1
sources:
  - apps/server/internal/apiref
  - apps/apiref/frontend
  - apps/server/internal/openapi/integrator
  - apps/server/internal/module/apidocument.go
  - apps/server/internal/module/apikeygate.go
  - apps/server/internal/module/apimcp.go
---

Vantigo's API is the same HTTP API its own screens use: JSON over HTTPS, one set of paths per
module under `/api/v1/<module>/`, described by OpenAPI. Another application — a web shop, a
reporting tool, a script — calls it as an **integration**, with one of the integration's
**API keys**. This page is for the person writing that application. How an Owner makes an
integration and its keys is under [API access](/en/admin/api-access/).

## Authenticating

Send the key in the `Authorization` header of every request, with the `Bearer` scheme:

```sh
curl -H "Authorization: Bearer vtg_…" https://vantigo.example.com/api/v1/customers
```

- The address is the installation's own `APP_URL`, with its base path if it has one.
- A key starts with `vtg_` and has 53 characters. It is read from that header only, never
  from a query string or a cookie, and a request carries exactly one `Authorization` header.
- A request with a key is the key's alone: a session cookie sent beside it is not read.
- Every key expires. An Owner rolls a key before it does, and the old key keeps working for an
  overlap the Owner chooses, so a client can switch without downtime.
- A key reaches what its integration's roles grant, cut down to the key's narrowing when it
  has one; a read-only key calls reads only. Some operations are never available to a key:
  a person's own account, an Owner's administration, SSO, SCIM, the keys themselves, and what
  moves money or reveals personal data in bulk.

## The document of what a key reaches

`GET /api/openapi.json` with the key answers an OpenAPI 3.0 document of exactly the
operations that key can call, ready for a client generator:

```sh
curl -H "Authorization: Bearer vtg_…" https://vantigo.example.com/api/openapi.json > vantigo.json
```

Each operation's description ends with the permission it requires, for example
*Requires `customers:view`*, and declares the `401`, `403` and `429` answers below. `servers`
is the installation's address, and the `bearerAuth` scheme is the key. The document follows
the key: when its roles, narrowing or read-only switch change, so does the document. A person
signed in to Vantigo gets every operation a key can call with `?view=api`.

The generated API reference on <https://docs.vantigo.io>, one section per module in the
sidebar, describes every module's full contract, including the operations no key can call.

## Trying it at /api

Every installation serves an interactive reference at `/api` — for example
`https://vantigo.example.com/api` — from the binary itself. Paste a key into **API key** to see
what it reaches and to send real requests with **Test Request**; signed in without a key, you
see every operation a key can call, and a request you try goes with your own session. The key
stays in the page and is never stored. Nothing on the page is fetched from outside the
installation. See [Trying the API at /api](/en/admin/api-access/#trying-the-api-at-api).

## Connecting an AI client

An AI client that speaks the Model Context Protocol connects to `POST /api/mcp` with the same
key, once an Owner has turned MCP on for the integration. It reaches the same operations through
its tools — search, describe, read and, when the integration allows writing and the key is not
read-only, write and delete — every call judged, counted and logged as the REST request it makes. See the
[MCP server](/en/reference/mcp/).

## Refusals

A refused request answers JSON in this shape:

```json
{ "error": { "code": "api_key_read_only", "message": "This API key may only read." } }
```

Branch on `error.code`; the message is for people and may change.

| Status | Code | When |
| --- | --- | --- |
| `401` | `unauthenticated` | The key is malformed, unknown or revoked, its integration is disabled or archived, the request came from outside the key's networks, or it carried several `Authorization` headers |
| `401` | `api_key_expired` | The key matched but has expired |
| `403` | `forbidden` | The key does not reach the permission the operation requires |
| `403` | `api_not_available` | No key may call the operation |
| `403` | `api_key_read_only` | A read-only key called an operation that is not a read |
| `429` | `rate_limited` | The integration has used every request its limit allows this minute |

A `401` carries `WWW-Authenticate: Bearer realm="vantigo"`. Other answers — `400` for a request
the contract refuses, `404`, `409` and the rest — are each operation's own and are in the
document.

## The rate limit

Each integration has a limit of requests per minute, shared by all of its keys, so adding a
key does not raise it. Every answer to a request the key is let through for — loading
`/api/openapi.json` included — carries:

| Header | Meaning |
| --- | --- |
| `RateLimit-Limit` | The requests the integration may send per minute |
| `RateLimit-Remaining` | The requests left in this minute |
| `RateLimit-Reset` | Seconds until the minute ends |

Past the limit the answer is `429 rate_limited` with `Retry-After`, in seconds. Wait that long
before sending again rather than retrying at once. A few operations also keep a limit of their
own, per address.

## What changes, and how you hear of it

Within `/api/v1` every change is additive: new operations, new optional request fields, new
response fields and new enum values in responses. Ignore what you do not know. A breaking change
ships as a new operation or a new version, and a deprecation is announced at least six months
before removal. The whole promise, and what it does not cover, is
[the compatibility policy](/en/reference/api-compatibility/).
