---
title: "OAuth connections"
description: "Connecting an MCP client or app by signing in, not with a key: OAUTH_ENABLED, the discovery documents, clients identified by a metadata document and the rules they are held to, redirect URIs, the authorization request and the consent operations, the token endpoint and its errors, the lifetimes, refresh and its grace, what a connection may do, the refusals and their challenges, what ends a connection, the request log and the upkeep."
sidebar:
  order: 5
sources:
  - apps/server/internal/identity/oauth_access.go
  - apps/server/internal/identity/oauth_authorize.go
  - apps/server/internal/identity/oauth_client.go
  - apps/server/internal/identity/oauth_connections.go
  - apps/server/internal/identity/oauth_token.go
  - apps/server/internal/identity/queries/oauth.sql
  - apps/server/internal/db/migrations/00218_identity_oauth.sql
  - apps/server/internal/server/oauth_discovery.go
  - apps/server/internal/modtest/oauth.go
---

An MCP client or another app can connect to Vantigo by having a person sign in and approve it,
instead of being handed an API key: the client is sent to Vantigo, an Owner signs in, picks the
[integration](/en/reference/api-introduction/) the client may act as and approves, and the
client then holds a one-hour **access token** it renews on its own. The result is a
**connection**. A connection acts as its integration exactly as one of the integration's keys
does, and grants nothing a key of the same integration could not do. This page is the contract,
for a person writing or configuring a client; it follows OAuth 2.1 with PKCE, the protected
resource metadata of RFC 9728, the authorization server metadata of RFC 8414 and the client ID
metadata documents the MCP authorization specification describes.

:::caution[Switched off for now]
`OAUTH_ENABLED` is off by default until the consent page and the **Connected apps** section land
on the screens. Until then an installation that turns it on serves everything below, but a
person has no page to approve a client on. Use an [API key](/en/reference/mcp/#authentication)
meanwhile.
:::

## Switching it on

`OAUTH_ENABLED=1` ([Configuration](/en/admin/configuration/)) turns it on. Off, which is the
default for now, the discovery documents and the token endpoint answer `404`, the consent
operations answer `404`, an access token (`Bearer vta_…`) is refused `401` as an unknown key is,
and `/api/mcp`'s `401` is the plain one an API-key client expects. Connections already made are
kept, and work again when it is turned back on.

The **issuer** is `APP_URL` with its default port dropped, followed by `APP_BASE_PATH`, with no
trailing slash, for example `https://vantigo.example.com` or `https://example.com/erp`. The
**resource** is the issuer followed by `/api/mcp`. Every URL below is built from them, so a
client compares exactly one string.

## Discovery

The documents are answered as `application/json` with `Cache-Control: public, max-age=300`:

| Path | Document |
| --- | --- |
| `/.well-known/oauth-protected-resource<base>/api/mcp`, and `/.well-known/oauth-protected-resource` | the protected resource's metadata |
| `/.well-known/oauth-authorization-server<base>`, and `<base>/.well-known/oauth-authorization-server` | the authorization server's metadata |

`<base>` is `APP_BASE_PATH`, empty at the domain root. With a base path the host-root forms lie
outside it: the proxy in front must send `/.well-known/oauth-*` to Vantigo
([Installation](/en/admin/installation/)). With `MCP_ENABLED` off the protected resource's
documents answer `404`; the rest stays. Every other path under `/.well-known/` that Vantigo
does not serve answers the `404` problem, never the application's page: a `security.txt` or an
ACME challenge is the proxy's to answer, in front of Vantigo.

The protected resource's metadata:

```json
{
  "resource": "https://vantigo.example.com/api/mcp",
  "authorization_servers": ["https://vantigo.example.com"],
  "scopes_supported": ["read", "write"],
  "bearer_methods_supported": ["header"],
  "resource_name": "Vantigo"
}
```

The authorization server's metadata:

```json
{
  "issuer": "https://vantigo.example.com",
  "authorization_endpoint": "https://vantigo.example.com/oauth/authorize",
  "token_endpoint": "https://vantigo.example.com/api/oauth/token",
  "response_types_supported": ["code"],
  "response_modes_supported": ["query"],
  "grant_types_supported": ["authorization_code", "refresh_token"],
  "code_challenge_methods_supported": ["S256"],
  "token_endpoint_auth_methods_supported": ["none"],
  "scopes_supported": ["read", "write", "offline_access"],
  "client_id_metadata_document_supported": true,
  "authorization_response_iss_parameter_supported": true
}
```

There is no registration endpoint: Vantigo does not do Dynamic Client Registration. There is no
OpenID Connect either — no ID token and no `userinfo` — and no JWKS: tokens are opaque.

## Clients

A client is identified by a **client ID metadata document**: its `client_id` is an `https` URL,
and the document at that URL describes it. Claude, Claude Code and ChatGPT identify themselves
this way.

A `client_id` is accepted when it is an `https` URL of at most 512 characters, on port 443, with
a host name in ASCII (a host spelled in punycode is refused, as is an IP address), no user, no
fragment, a path other than `/` with no `.` or `..` segment, and a host that is not this
installation's own — the host names of `APP_URL` and `PUBLIC_BASE_URL`; another name that
reaches the same server is not recognised as its own. Vantigo then fetches the document:

- `GET` with `Accept: application/json` and `User-Agent: Vantigo`, through the same guard as
  webhooks: an address that is private, loopback, link-local or otherwise special is never
  dialled, no proxy is used, and **no redirect is followed**;
- in at most 5 seconds in all, reading at most 16 KiB after decompression;
- `200` and a JSON object only.

The document is accepted when its `client_id` equals the URL exactly; its `client_name` is a
non-empty string; its `redirect_uris` are 1 to 20 strings, each a valid redirect URI (below); it
carries no `client_secret`; its `grant_types`, when present, include `authorization_code`; its
`response_types`, when present, include `code`; and the client can connect as a public client:
when `token_endpoint_auth_methods_supported` is present it must include `none` (and
`token_endpoint_auth_method` is then ignored), otherwise `token_endpoint_auth_method` must be
absent or `none`. ChatGPT's document declares `private_key_jwt` and lists `none` among the
methods it supports, so it is accepted as a public client. Every other member — a logo, a home
page, a `jwks_uri` — is ignored and never fetched or shown.

A good document is kept for its `Cache-Control: max-age`, at least 5 minutes and at most 24
hours, one hour when it has none. A failure is never kept; one `client_id` is fetched at most
once in 10 seconds by each server process. A document is fetched only while an Owner previews or
approves a request (below), never for an anonymous caller.

### Redirect URIs

A redirect URI is `https` with a host, or `http` with the host `127.0.0.1`, `[::1]` or
`localhost`; it has no fragment and at most 1,024 characters. An authorization request's
`redirect_uri` must match one the document lists: an `https` URI by exact string comparison; an
`http` loopback URI by its scheme, host, path and query, with the port ignored (a listed URI
without a port matches any). `localhost` and `127.0.0.1` are different hosts.

## The authorization request

The client sends the person's browser to `<issuer>/oauth/authorize` with:

| Parameter | Rule |
| --- | --- |
| `response_type` | `code` |
| `client_id` | the client's metadata document URL |
| `redirect_uri` | one the document lists |
| `code_challenge` | required: 43 characters of `[A-Za-z0-9_-]` |
| `code_challenge_method` | `S256` |
| `scope` | `read`, `write` and `offline_access`, separated by spaces; none, or only `offline_access`, means `read` |
| `state` | optional, at most 512 characters, returned as sent |
| `resource` | optional; when sent, the resource or the issuer (a trailing slash, letter case in the scheme and host, and a default port are tolerated) |

Each parameter appears at most once. `write` includes `read`. `offline_access` is accepted and
changes nothing: a refresh token is always issued.

The client and the redirect URI are judged first. A `client_id` that is refused, a document that
cannot be fetched or breaks a rule, and a `redirect_uri` the document does not list are shown to
the person as a refused client, and the browser is **never** sent back to the client. Anything
else wrong is the client's to be told, and the browser goes back to the redirect URI with
`error` — `unsupported_response_type`, `invalid_request` (no PKCE, a method other than `S256`, a
repeated parameter, a `state` over its bound), `invalid_scope` or `invalid_target` — and
`error_description`, `state` and `iss`. It goes back at once only when the redirect URI's host is
the `client_id`'s host or a loopback one **and** a client of that host has been connected here
before; otherwise the page shows the error and a button that returns to that host, so a crafted
request cannot bounce a signed-in Owner to a host nobody has approved.

### The consent operations

The consent page is the application's own; it reads and answers the request through two
operations of the identity contract, both `policy:Owner+OwnerManagement` (the Owner-MFA rule
applies), neither available to an API key nor to the Assistant:

- `POST /api/v1/identity/oauth/authorizations/preview` with `{"query": "<the query string>"}`
  answers `client_refused`, `redirect` (with `redirectTo` and whether it is `automatic`) or
  `consent`: the name the client calls itself (control and bidirectional characters stripped,
  cut at 80 characters), the host Vantigo fetched its document from, the redirect URI's host and
  whether it is loopback, the scopes asked, whether a client of this host has been connected
  before, whether the name it claims contains "Vantigo" or the workspace's name (`APP_TITLE`,
  when it is set), and the integrations with whether each is **eligible** — active, and its MCP
  setting not `off` — and the most a connection to it may do. Previews and approvals together
  are limited to 20 a minute per person (`429` with `Retry-After`), since each may fetch a
  client's document.
- `POST /api/v1/identity/oauth/authorizations` with the same `query`, `decision` (`approve` or
  `deny`), and to approve `integrationId` and `access` (`read` or `read_write`, at most what the
  request asks and the integration's MCP setting allows) judges everything again and answers
  `{"redirectTo": "…"}` with `Cache-Control: no-store`. Deny sends the client `access_denied`.
  Approve creates the connection, not yet active, and a **code** valid 60 seconds, stored only as
  a hash, and the redirect carries `code`, `state` and `iss`. A refused client is `400
  client_refused`; an integration that is not eligible, or an access above what it allows, is
  `409 integration_not_eligible`.

A person who is not an Owner gets `403` from both, and the page tells them to ask an Owner.

## The token endpoint

`POST <issuer>/api/oauth/token` takes `application/x-www-form-urlencoded` only, at most 16 KiB,
each parameter at most once and at most 1,024 characters. It is anonymous, reads no cookie and
is not behind the cross-origin protection, so a client that sends `Origin` gets an answer. It is
limited to 120 requests a minute per address. Every answer is JSON with `Cache-Control:
no-store`; an error is RFC 6749's `{"error", "error_description"}`, `400` except
`invalid_client`, which is `401`.

A client authenticates with PKCE alone: `Authorization: Basic`, a `client_secret` or a
`client_assertion` is `invalid_client`. A `resource`, when sent, must be the resource or the
issuer (`invalid_target`).

**Exchanging a code** — `grant_type=authorization_code` with `code`, `client_id`,
`redirect_uri` and `code_verifier` (43 to 128 characters of `[A-Za-z0-9._~-]`), all required.
The code is spent the moment it is presented. Its `client_id` and `redirect_uri` must equal the
request's at authorization, and `BASE64URL(SHA256(code_verifier))` its `code_challenge`;
otherwise `invalid_grant`, and the code is gone. Success activates the connection — audited
`connection.authorized` — and issues a token pair. **A code presented a second time revokes its
connection** (`code_replayed`) and is `invalid_grant`, so the tokens the first exchange issued
stop too.

**Refreshing** — `grant_type=refresh_token` with `refresh_token` and `client_id` (the
connection's). A `scope`, when sent, may not ask for more than the connection holds
(`invalid_scope`) and changes nothing. Each refresh issues a new access token and a new refresh
token and spends the old refresh token:

- a spent refresh token presented again **within 60 seconds** is honoured once more, for a
  client that lost the answer; the pair the first refresh issued is then superseded, so a
  connection never has two live refresh tokens;
- a superseded refresh token presented later **revokes the connection** (`refresh_reused`): two
  parties hold the family, and Vantigo cannot tell the client from a thief who replayed its
  token. The cost is a new sign-in for a client whose two processes race one refresh and then
  both keep going;
- a spent refresh token presented **after the 60 seconds** revokes the connection
  (`refresh_reused`) and is `invalid_grant`.

A spent token is judged as a replay before anything else about the request, whatever
`client_id` or `scope` it is sent with: whoever presents it holds it. An unspent token sent with
another `client_id` or a wider `scope` is refused and spends nothing. A refresh of a connection
whose tokens would be refused anyway — its approver no longer an active Owner, its integration
archived or disabled — is `invalid_grant`, and the token is left as it was.

Refreshes of one connection queue on the connection, so two at once never both spend the same
token: the second is the first one's retry.

A token pair is answered as:

```json
{
  "access_token": "vta_…",
  "token_type": "Bearer",
  "expires_in": 3600,
  "refresh_token": "vtr_…",
  "scope": "read write"
}
```

Other errors: `unsupported_grant_type` for a grant other than the two, `invalid_request` for a
missing, repeated or malformed parameter or another body, and `invalid_grant` for a code or
refresh token that is unknown, expired, spent or whose connection has ended.

### Tokens and lifetimes

Tokens are opaque: `vta_` (access) and `vtr_` (refresh), each 53 characters — the API key's
encoding, with a checksum a scanner can verify. Only their hashes are stored. A refresh token is
never accepted as an access token, nor the other way round.

| What | Lifetime |
| --- | --- |
| A code | 60 seconds, once |
| An access token | 1 hour |
| A refresh token | 30 days from when it was issued |
| A connection | 180 days from consent |

Every token expires at the earlier of its own lifetime and its connection's end, and
`expires_in` says so. After 180 days the client is sent to sign in again.

## Presenting a token

The client sends the access token as an API key is sent: `Authorization: Bearer vta_…`, on
`/api/mcp` and on every REST operation, since MCP's tool calls are REST requests of the
connection's own. A request with a token is the token's alone: a session cookie beside it is not
read, and a refusal never clears it.

A connection is its integration. Its requests run through the key's checks — operations marked
never available to API keys are `403 api_not_available`; the integration's roles, less what is
never available to a key, decide the rest; it has no narrowing and no networks — and they are
counted against the integration's rate limit, shared with its keys. A connection approved for
**Read only** (no `write` scope) is a read-only key.

The [Idempotency-Key](/en/reference/api-introduction/#retrying-safely) is scoped to the
integration, so its keys and its connections share it: two clients that send the same key for
different requests get `422 idempotency_key_reused`.

At `/api/mcp` the effective mode is the narrower of the integration's MCP setting and the
connection's scope; an integration whose setting is `off` is `403 mcp_not_enabled`, while REST
still works. Each tool's descriptor carries `securitySchemes` with the scope it needs — `read`
for `search_operations`, `describe_operation` and `read`, `write` for `write` and `delete`. At most
four tool calls of one connection run at once, as for one key.

### Refusals

| Answer | When | `WWW-Authenticate` |
| --- | --- | --- |
| `401 token_invalid` | the token is unknown or expired; its connection is revoked, expired or was never activated; the integration is archived or disabled; or the person who approved it is no longer an active Owner | `Bearer error="invalid_token", resource_metadata="<the protected resource's metadata URL>"` |
| `403 insufficient_scope` | a Read-only connection called an operation that is not a read, and the integration's roles would allow it | `Bearer error="insufficient_scope", scope="read write", resource_metadata="…"` |
| `403 forbidden`, `403 api_not_available` | the integration's roles, or the operation's mark, refuse it, as for a key | none |
| `401 api_key_required` at `/api/mcp` | no `Bearer vtg_…` or `Bearer vta_…` at all | `Bearer realm="vantigo", resource_metadata="…", scope="read write"` |

The metadata URL is the host-root form, `<origin>/.well-known/oauth-protected-resource<base>/api/mcp`.
With `MCP_ENABLED` off that document is not served, and no challenge names it: a token's `401`
is `Bearer error="invalid_token"` alone. A client without a token that reads the last challenge
starts a sign-in.

## What ends a connection

- **Revoke** by an Owner: `DELETE /api/v1/identity/integrations/{id}/connections/{connectionId}`
  (`revoked`). `GET /api/v1/identity/integrations/{id}/connections` lists the activated
  connections that are neither revoked nor expired, newest first, each `active` or `inactive`.
  Both are `policy:Owner+OwnerManagement` and answer `404` while `OAUTH_ENABLED` is off.
- **Archiving the integration** revokes them in the same transaction as its keys
  (`integration_archived`).
- **The person who approved it** stops qualifying: every request checks that they are a person,
  not disabled, not pending, an Owner now, and, while SCIM is on, not deactivated by the
  directory — a password lockout does not count. Their deletion, their disable and their Owner
  role's removal revoke the connection (`authorizer_removed`); a change that reaches them another
  way, such as a group mapping, leaves the connection listed as `inactive` with the reason
  `authorizer_removed`, its tokens refused, until an Owner revokes it.
- **A replay**: a code exchanged twice (`code_replayed`), a refresh token reused after its grace
  (`refresh_reused`).
- **Expiry**, 180 days after consent.

Two approvals of the same client and integration make two connections: every Claude Code
install shares one `client_id`, and a second computer does not end the first. Each ending is
audited `connection.revoked` with its reason; no token, code or verifier is ever recorded.

## The request log

A connection's requests are in its integration's [request log](/en/admin/api-access/#the-request-log)
beside its keys': a row names the connection (`connectionId`, and the client's name) instead of a
key (`keyId` is `null`). Refusals add the reasons `insufficient_scope` and `token_invalid` (a
connection never activated, or whose approver no longer qualifies) to the key's.

## Upkeep

The `identity.api-keys` worker runs when `IDENTITY_API_KEYS_ENABLED`, `IDEMPOTENCY_ENABLED` or
`OAUTH_ENABLED` is on, and prunes, under a lease of its own and whatever `OAUTH_ENABLED` says:
codes an hour after they were spent or expired (so a replay still finds its connection), tokens a
day past their expiry, connections never activated 10 minutes after they were approved,
connections 90 days after they were revoked or expired, and cached documents a week past their
expiry.
