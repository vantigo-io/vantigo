---
title: "API introduction"
description: "Calling Vantigo's API as an integration: authenticating with an API key, the document of what a key reaches, the error codes a refusal carries, the rate limit and its headers, retrying safely with an Idempotency-Key, the interactive reference at /api, connecting an AI client over MCP, and the compatibility promise."
sidebar:
  order: 1
sources:
  - apps/server/internal/apiref
  - apps/apiref/frontend
  - apps/server/internal/openapi/integrator
  - apps/server/internal/module/apidocument.go
  - apps/server/internal/module/apikeygate.go
  - apps/server/internal/module/apimcp.go
  - apps/server/internal/module/idempotency.go
  - apps/server/internal/server/oauth_discovery.go
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
*Requires `customers:view`*, and declares the `401`, `403` and `429` answers below. Every
operation that honours an `Idempotency-Key` ([Retrying safely](#retrying-safely)) declares it
as an optional header, with the `409` and `422` it can answer: added to the operation's own
`409` or `422` where it documents one — its description first, the error body offered beside
its schema (`anyOf`) unless that schema already takes the error body, and with `Retry-After` on
the `409` — and declared on their own where it does not. With `IDEMPOTENCY_ENABLED` off no
operation declares it, since none honours it. `servers`
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

## A token beside a key

An integration can also be reached without a key being copied anywhere: a client connects by
having an Owner sign in and approve it, and then holds a one-hour access token, `Bearer vta_…`,
that it renews on its own. A token is accepted wherever a key is, on `/api/mcp` and on REST, and
acts as its integration exactly as a key does, with the integration's roles and rate limit; a
connection approved for Read only is a read-only key. It is on by default and switched off
with `OAUTH_ENABLED=0`. See [OAuth connections](/en/reference/oauth/).

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
| `401` | `token_invalid` | An access token is unknown or expired, or its connection has ended ([OAuth connections](/en/reference/oauth/#refusals)) |
| `403` | `insufficient_scope` | A connection approved for Read only called an operation that is not a read |

A key's `401` carries `WWW-Authenticate: Bearer realm="vantigo"`; a token's carries
`Bearer error="invalid_token"` with the protected resource's metadata URL. Other answers — `400` for a request
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

## Retrying safely

A request that creates something — an invoice, a mail, a shipment — and times out may or may not
have been carried out. Send it with an `Idempotency-Key` header, a value you make up once for that
one action (a UUID will do), and send **the same key with every retry of that action**. Vantigo
then carries the action out once, and answers every retry with the first answer.

```sh
curl -X POST -H "Authorization: Bearer vtg_…" -H "Idempotency-Key: 8e03978e-40d5-43e8-bc93-6894a57f9324" \
  -H "Content-Type: application/json" -d '{"name":"Nordvik AS"}' https://vantigo.example.com/api/v1/customers
```

- **Where it works.** On every `POST` a key can call that creates or changes something and takes
  and answers JSON: not on a read marked as one (a search, a preview), not on a file upload or a
  download, not on one that answers a secret once, and not on the staff retention run, which can
  run longer than two minutes. Elsewhere the header is ignored. The header is honoured exactly
  where the live document of what a key reaches (`/api/openapi.json` as the installation serves
  it now) declares it; rely on that, not on a copy. An installation with `IDEMPOTENCY_ENABLED` off
  declares and honours it nowhere, and answers a request to one of these operations that carries
  a key with `Idempotency-Key-Ignored: true`, so a retry there runs again. Three communications
  operations and applying a credit note keep a key of their own, described on each. It is never
  required.
- **The key** is 1–255 printable ASCII characters, optionally in double quotes; anything else is
  `400 idempotency_key_invalid`. A key belongs to the integration (across all its keys) and to the
  operation: the same key on another operation, or from another integration, is another key.
- **The same request** is the same path, query, `Content-Type` (its media type and charset, so
  `application/json` and `application/json; charset=utf-8` are one), `If-Match`, `If-None-Match`
  and body. A JSON body of up to 64 KiB is compared by its content, so another key order or other
  spacing is the same; a larger body, or one that is not valid UTF-8, is compared byte for byte,
  so a reformatted large body is a different request.
- **The replay** answers the first answer's status, its `Content-Type`, `Location`, `ETag` and
  `Content-Language`, and its body, with `Idempotent-Replayed: true`. A replay is counted against
  the rate limit and is in the request log with the reason *Replayed for its Idempotency-Key*.
  Successes (`2xx`) and the refusals `400`, `404`, `410` and `422` the operation answers as its
  own are replayed. A `409` is not kept, nor is a refusal that is not the operation's to keep (a
  malformed body, an answer marked `Cache-Control: no-store`): the same request with the same key
  runs again, once you have resolved the conflict or fixed the body.
- **A replay is the first answer as it was.** It is not checked again against the record: if the
  record has changed since, or the integration lost the permission, the retry still gets the
  first answer, within the 24 hours. It is the stored answer to the first call with the key,
  whichever of the integration's keys made it: a retry from another of its keys must still be
  allowed to call the operation, but the answer is not cut to that key's narrowing.
- **Kept 24 hours.** After that the key is free again. The first answer — status, headers and
  body — is kept encrypted. When any customer is anonymised, the answers kept before are not given
  again: a retry gets `idempotency_outcome_unknown`.

| Status | Code | When |
| --- | --- | --- |
| `400` | `idempotency_key_invalid` | The header is not one valid key |
| `409` | `idempotency_in_progress` | The first request with the key is still running; `Retry-After: 1` |
| `409` | `idempotency_outcome_unknown` | The first request ended without an answer that can be given again (a `5xx`, a timeout, a lost connection, an answer that is not kept): it may or may not have been carried out. Read the record, then use a new key |
| `422` | `idempotency_key_reused` | The key was used for a different request |
| `429` | `idempotency_quota` | A new key, while the integration keeps 2,000 live keys or 16 MB of answers already (a request still running counts as 256 KB); `Retry-After: 60`. A key it already has is still answered |

Vantigo never runs a request again for a key whose first request reached the operation: when it
cannot tell what happened, it says so with `idempotency_outcome_unknown` instead. A first request
still running two minutes after it began is answered that way too; should it then finish, later
retries get its answer.

## What changes, and how you hear of it

Within `/api/v1` every change is additive: new operations, new optional request fields, new
response fields and new enum values in responses. Ignore what you do not know. A breaking change
ships as a new operation or a new version, and a deprecation is announced at least six months
before removal. The whole promise, and what it does not cover, is
[the compatibility policy](/en/reference/api-compatibility/).
