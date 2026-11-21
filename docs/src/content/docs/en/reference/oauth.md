---
title: "OAuth connections"
description: "Connecting an MCP client or app by signing in, not with a key: OAUTH_ENABLED, the discovery documents, clients identified by a metadata document and the rules they are held to, clients registered by hand, the client hosts that may connect, redirect URIs, the authorization request and the consent operations, the token endpoint and its errors, the lifetimes, refresh and its grace, the revalidation of a document, the revocation endpoint, what a connection may do, the refusals and their challenges, what ends a connection, the request log and the upkeep."
sidebar:
  order: 5
sources:
  - apps/server/internal/identity/oauth_access.go
  - apps/server/internal/identity/oauth_authorize.go
  - apps/server/internal/identity/oauth_client.go
  - apps/server/internal/identity/oauth_connections.go
  - apps/server/internal/identity/oauth_token.go
  - apps/server/internal/identity/oauth_clients.go
  - apps/server/internal/identity/oauth_revalidate.go
  - apps/server/internal/identity/oauth_revoke.go
  - apps/server/internal/identity/queries/oauth.sql
  - apps/server/internal/identity/queries/oauth_clients.sql
  - apps/server/internal/db/migrations/00218_identity_oauth.sql
  - apps/server/internal/db/migrations/00219_identity_oauth_clients.sql
  - apps/server/internal/server/oauth_discovery.go
  - apps/server/internal/modtest/oauth.go
  - apps/server/internal/config/config.go
  - apps/host/frontend/src/routes/oauth.authorize.tsx
  - apps/host/frontend/src/lib/oauth-return.ts
  - apps/host/frontend/src/api/oauth.ts
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

How an Owner connects Claude Code, Claude, ChatGPT, Cursor or VS Code, what the consent page
shows and how a connection is revoked are in the
[administration guide](/en/admin/api-access/#connecting-by-signing-in).

## Switching it off

It is on by default. `OAUTH_ENABLED=0` ([Configuration](/en/admin/configuration/)) switches it
off: the discovery documents, the token endpoint and the revocation endpoint answer `404`, the
consent operations, the connection operations, the registered clients' operations and the client
hosts' answer `404` (the consent page says so, and the screens show no **Connected apps**,
**Registered clients** or **Client hosts**), an access token (`Bearer vta_…`) is refused `401` as an unknown key is,
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
  "revocation_endpoint": "https://vantigo.example.com/api/oauth/revoke",
  "revocation_endpoint_auth_methods_supported": ["none"],
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

A client is identified in one of two ways:

- by a **client ID metadata document**: its `client_id` is an `https` URL, and the document at
  that URL describes it. Claude, Claude Code and ChatGPT identify themselves this way;
- as a **registered client**: an Owner registered it here, and its `client_id` is one Vantigo
  minted, `vtc_` and 24 letters and digits ([Registered clients](#registered-clients)). For a
  client that publishes no document, such as Cursor and VS Code.

A `client_id` that starts with `vtc_` is only ever looked up among the registered clients, never
fetched.

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
once in 10 seconds by each server process. A document is fetched while an Owner previews or
approves a request (below), and again, in the background, when one of its connections refreshes
after the cached copy has expired ([Revalidation](#revalidation)); never for a caller who holds
neither a session nor a connection.

### Registered clients

An Owner registers a client by hand for an app that publishes no document. The operations are
`policy:Owner+OwnerManagement` (the Owner-MFA rule applies), available neither to an API key nor
to the Assistant, and answer `404` while `OAUTH_ENABLED` is off:

| Operation | What it does |
| --- | --- |
| `GET /api/v1/identity/oauth-clients` | the clients not archived, oldest first, each with who registered it and how many of its connections are live |
| `POST /api/v1/identity/oauth-clients` | registers one: `201` with its minted `client_id`; at most 50 at once (`409 oauth_client_limit`); audited `oauth_client.created` |
| `PATCH /api/v1/identity/oauth-clients/{id}` | replaces its name and redirect URIs under `If-Match` on its `version` (`412 version_mismatch`); a connection whose redirect URI the new list no longer matches is revoked in the same transaction (`client_changed`); audited `oauth_client.updated` |
| `POST /api/v1/identity/oauth-clients/{id}/archive` | archives it under `If-Match` and revokes every connection it has in the same transaction (`client_archived`); `204`; it cannot be undone; audited `oauth_client.archived` |

The body of a registration or an edit is `{"name", "redirectUris"}`. The name is 1 to 80
characters once control and bidirectional characters are removed; the redirect URIs are 1 to 20,
each held to the rules a document's are ([Redirect URIs](#redirect-uris)), repeats dropped. A
body that breaks a rule is `400 invalid_request` with the field at fault (`name`,
`redirectUris`); the message never repeats what was sent. An archived client, or one never
registered, is `404 not_found`.

A registered client is a public client like any other: PKCE, `none` at the token endpoint, no
secret. On the consent page it is shown as **Registered in this workspace by** the person who
registered it — named only while that person is still an active Owner, by the same rule an
approver is held to; otherwise just **Registered in this workspace** (`registeredBy` is `null`) —
instead of the host a document was read from, and the host shown and kept on its connections is
the redirect URI's. Its redirect URIs are an Owner's own, so an error is always redirected
without a click, and there is no first-use notice or name warning.

The client's row is held through the approval: the approval's transaction reads it `FOR SHARE`,
against the archive's and the edit's `FOR UPDATE`, and refuses (`400 client_refused`) a client
archived or no longer listing the request's `redirect_uri` meanwhile, so no connection is made for
either. The token endpoint also refuses (`invalid_grant`) an exchange or a refresh of a registered
client's connection whose client is archived. An **edit** that removes a redirect URI revokes, in
its transaction, every connection whose redirect URI the new list no longer matches — compared as
at sign-in, a loopback port ignored — with the reason `client_changed`, each audited
`connection.revoked`; the others are kept.

### Client hosts

An Owner can limit which hosts' clients may connect by their document, with the setting
`oauth_client_hosts` on the identity settings:

- `GET /api/v1/identity/oauth/settings` answers `{"clientHosts": [...]}`, lower-case and sorted;
- `PUT /api/v1/identity/oauth/settings` with `{"clientHosts": [...]}` replaces it. Each entry is a
  DNS name of at least two labels in ASCII — no address, port, wildcard or punycode — and there
  are at most 50; case and repeats are ignored. Anything else is `400 invalid_request` on
  `clientHosts`. The list already held writes nothing; a change is audited
  `oauth.client_hosts_changed` with the list before and after.

Both are `policy:Owner+OwnerManagement`, never for an API key or the Assistant, and `404` while
`OAUTH_ENABLED` is off.

**Empty**, the default, admits any client identified by a document. Otherwise the `client_id`'s
host must **equal an entry or be a subdomain of one**: `claude.ai` admits `claude.ai` and
`api.claude.ai`, not `notclaude.ai`. **Registered clients are always admitted.** The list is held:

- at the preview and the approval, **before** the document is fetched: a host it leaves out is
  a refused client and is never fetched;
- at **every refresh**: a connection whose host it leaves out is `invalid_grant` ("This workspace
  no longer admits this client's host."), with the refresh token left unspent and nothing
  revoked. An access token already issued works until it expires, within the hour.

It is not held again at the code exchange: a code approved just before the list changed can
still be exchanged within its 60 seconds, and the connection is then held at its first refresh.
An entry admits every subdomain, so a shared hosting domain — `github.io`, `vercel.app`,
`pages.dev` — admits every tenant's site on it.

Changing the list **does not end** a connection: one whose host is left out is listed as
`inactive` with the reason `client_not_allowed`, and renews again once its host is listed.

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
| `client_id` | the client's metadata document URL, or a registered client's `vtc_…` |
| `redirect_uri` | one the document, or the registered client, lists |
| `code_challenge` | required: 43 characters of `[A-Za-z0-9_-]` |
| `code_challenge_method` | `S256` |
| `scope` | `read`, `write` and `offline_access`, separated by spaces; none, or only `offline_access`, means `read` |
| `state` | optional, at most 512 characters, returned as sent |
| `resource` | optional; when sent, the resource or the issuer (a trailing slash, letter case in the scheme and host, and a default port are tolerated) |

Each parameter appears at most once. `write` includes `read`. `offline_access` is accepted and
changes nothing: a refresh token is always issued.

The client and the redirect URI are judged first. A `client_id` that is refused, a registered
client that is unknown or archived, a host the [client hosts](#client-hosts) leave out, a
document that cannot be fetched or breaks a rule, and a `redirect_uri` the client does not list
are shown to the person as a refused client, and the browser is **never** sent back to the client. Anything
else wrong is the client's to be told, and the browser goes back to the redirect URI with
`error` — `unsupported_response_type`, `invalid_request` (no PKCE, a method other than `S256`, a
repeated parameter, a `state` over its bound), `invalid_scope` or `invalid_target` — and
`error_description`, `state` and `iss`. It goes back at once for a registered client, and for a
document's client only when the redirect URI's host is the `client_id`'s host or a loopback one
**and** a client of that host has been connected here before; otherwise the page shows the error and a button that returns to that host, so a crafted
request cannot bounce a signed-in Owner to a host nobody has approved.

### The consent operations

The consent page is the application's own; it reads and answers the request through two
operations of the identity contract, both `policy:Owner+OwnerManagement` (the Owner-MFA rule
applies), neither available to an API key nor to the Assistant:

- `POST /api/v1/identity/oauth/authorizations/preview` with `{"query": "<the query string>"}`
  answers `client_refused`, `redirect` (with `redirectTo` and whether it is `automatic`) or
  `consent`: the client's `kind` (`metadata` or `registered`, with `registeredBy` for the
  latter), the name the client calls itself (control and bidirectional characters stripped,
  at most two combining or enclosing marks on a character, cut at 80 characters), the host Vantigo fetched its document from (for a registered client, the redirect URI's), the redirect URI's host and
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

A person who is not an Owner gets `403` from both.

### The consent page

`<issuer>/oauth/authorize` is a page of the application (`routes/oauth.authorize.tsx`), open
without a session so it can act on its own. It reads the query string as the browser received
it, before the application's router can rewrite the address, posts it unchanged, and puts it
back in the address bar once the router has rewritten it, so a reload reads the request as sent
(a `state` of `1e3` stays `1e3`). It never builds a redirect of its own: it only ever sends the
browser to a `redirectTo` the server answered, and only when that is `https`, or `http` to
`127.0.0.1`, `[::1]` or `localhost`; anything else is shown as a failure and followed nowhere.

- **No session, or one that expires** before the preview or the decision (`401`). The page
  stores its path and query in `sessionStorage` under `oauth.return`, with the time, and goes to
  sign-in. After any sign-in — password, two-factor, passkey, OIDC —
  the application's root, once its session and MFA-enrolment gates pass, takes the entry and
  loads it again. The entry is removed when it is read and honoured only when it is younger than
  ten minutes and its path is exactly `/oauth/authorize` with a query: no other path, host,
  scheme or fragment. No URL carries a return-to.
- **A person who is not an Owner** is told to ask an Owner, with Close only, whatever their
  enrolment; the page calls neither operation, so no document is fetched and nothing is
  redirected.
- **An Owner held at MFA enrolment** is sent to the security settings with the entry stamped
  afresh. While that page holds them it starts the entry's ten minutes again, by keeping the
  time under `oauth.return.touched` beside the entry, which is never rewritten; once the
  authenticator is on it shows **Continue to the app's request** below the recovery codes, which
  takes the entry and loads the consent page. It never navigates on its own.
- **An Owner** sees the server's answer: a refused client with Close only; an error with an
  automatic redirect or a **Return to *host*** button, as `automatic` says; or the consent, with
  the claimed name labelled as a claim beside the two hosts, the name, loopback and first-use
  warnings, no integration preselected, and **Read only** unless **Read and write** is chosen
  where `maxAccess` allows it. **Approve** and **Deny** post the decision once, however often
  they are pressed, and follow its `redirectTo`; `409 integration_not_eligible` reads the
  preview again. A refusal is shown in the page's own words for its OAuth `error` code, with the
  server's English `message` beside them on an English page; a code the page does not know
  shows the `message`. **Close** closes the tab where the client opened it, and otherwise
  goes to the application's start page.

The page is never framed: every response carries `Content-Security-Policy: frame-ancestors
'none'` and `X-Frame-Options: DENY` (the latter alone under `CSP_REPORT_ONLY`), and the page
renders nothing when it finds itself inside a frame.

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
archived or disabled — or whose client's host the [client hosts](#client-hosts) now leave out
is `invalid_grant`, and the token is left as it was.

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
refresh token that is unknown, expired, spent or whose connection has ended. A registered
client's `client_id` sent as a code or a refresh token is `invalid_grant`, as any string is that
is not one.

### Revalidation

A document's client is checked again while it keeps renewing. When a connection of a client
identified by a document refreshes and the cached document has expired, the refresh is answered
as usual and the document is then fetched again **in the background**, by the rules of
[Clients](#clients). Only a document that speaks for the client can end its connection:

- **terminal** — the connection is **revoked** (`client_document_changed`, audited
  `connection.revoked` as `system:oauth`) and the tokens the refresh just issued stop at their
  next request — when the answer is a JSON object that names a **different**, non-empty
  `client_id`, or names **this** `client_id` and breaks a rule of [Clients](#clients) or no longer
  lists the connection's redirect URI;
- **transient** — nothing revoked and nothing cached; the next refresh after the cache has
  expired tries again — for anything else: a failure to read (the host unreachable, a timeout, a
  status other than `200`, a redirect, a body over 16 KiB), a `200` whose `Content-Type` is not
  `application/json` or a `+json` type, a body that is not a JSON object, or an object with no
  `client_id`. A maintenance page or a CDN's error body is not the client changing its mind;
- a document that **still holds** is cached again for its `max-age`.

A refresh while the cache is fresh fetches nothing. One `client_id` is fetched at most once in 10
seconds by each server process, and each process runs at most 8 revalidations at once, at most
one for each client host; a refresh that finds them busy starts none, and is logged at warning
level when all 8 are taken. On shutdown the server waits up to 5 seconds for those running. The fetch holds no database connection or transaction: the
cache is read before it, and the revocation or the cache write is a transaction of its own after.
A registered client is never revalidated: an Owner changes it by hand.

## The revocation endpoint

`POST <issuer>/api/oauth/revoke` ([RFC 7009](https://www.rfc-editor.org/rfc/rfc7009)) lets a
client end its own connection, as it signs out or removes the server. It is mounted like the token
endpoint: anonymous, no cookie read, not behind the cross-origin protection, limited to **120
requests a minute per address** (its own count beside the token endpoint's), `404` while
`OAUTH_ENABLED` is off. The body is `application/x-www-form-urlencoded`, at most 16 KiB, each
parameter at most once and at most 1,024 characters:

| Parameter | Rule |
| --- | --- |
| `token` | required: the connection's access token (`vta_…`) or refresh token (`vtr_…`) |
| `client_id` | required: the connection's |
| `token_type_hint` | optional, ignored: the prefix says which token it is |

Presenting **either** token with its connection's `client_id` revokes the **whole connection**
(`revoked_by_client`, audited `connection.revoked` as `system:oauth`): both tokens are refused
from their next request. An **access token** does so only until it expires; an expired one
revokes nothing. A **refresh token** does so whether it is live, spent, superseded or expired:
whoever holds any refresh token of the connection's family may end it, as a reused one ends it
at the token endpoint.

| Answer | When |
| --- | --- |
| `200`, empty body, `Cache-Control: no-store` | the connection was revoked, or was already ended; and also for a token that is unknown, malformed, an expired access token, of another kind (an API key, a `vtc_` client ID) or of another client's connection, for which nothing is revoked, so the answer says nothing about whether a token exists |
| `400 invalid_request` | another content type, a missing `token` or `client_id`, a repeated or too long parameter, a body over 16 KiB |
| `401 invalid_client` | `Authorization`, `client_secret` or `client_assertion` sent: the client is public |
| `405 invalid_request` | another method than `POST`, with `Allow: POST` |
| `429 invalid_request` | over the address's limit, with `Retry-After` |

Errors are RFC 6749's `{"error", "error_description"}` with `Cache-Control: no-store`.

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
  Both are `policy:Owner+OwnerManagement` and answer `404` while `OAUTH_ENABLED` is off. The
  integration's **Overview** shows the list as **Connected apps**, under MCP, with **Revoke**
  behind a confirmation (`routes/workspace/-api-access-connections.tsx`); it is not shown while
  the list answers `404`.
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
- **A registered client archived** (`client_archived`), in the archive's transaction, or **edited**
  so that it no longer lists the connection's redirect URI (`client_changed`), in the edit's.
- **A document that no longer holds** at its [revalidation](#revalidation)
  (`client_document_changed`).
- **The client itself**, through the [revocation endpoint](#the-revocation-endpoint)
  (`revoked_by_client`).
- **Expiry**, 180 days after consent.

Two approvals of the same client and integration make two connections: every Claude Code
install shares one `client_id`, and a second computer does not end the first. Each ending is
audited `connection.revoked` with its reason; no token, code or verifier is ever recorded.

## The request log

A connection's requests are in its integration's [request log](/en/admin/api-access/#the-request-log)
beside its keys': a row names the connection (`connectionId`, and the client's name) instead of a
key (`keyId` is `null`). Refusals add the reasons `insufficient_scope` and `token_invalid` (a
connection never activated, or whose approver no longer qualifies) to the key's.

`GET /api/v1/identity/integrations/{id}/requests?connection=<connection id>` reads one
connection's requests, as `key=` reads one key's; with both, only requests that match both,
which is none. A connection's requests are read by queries of their own, filtered on
`connection_id` plainly, so they read the index `ix_api_requests_connection_at` on
`(connection_id, at)`, kept for a connection's rows only (`00219_identity_oauth_clients.sql`).

`GET /api/v1/identity/integrations/{id}/connections?include=ended` lists, beside the live
connections, those that ended — revoked or expired — within the log's 30 days, each `ended` with
`endedAt` and `endReason` (the reason it was revoked with, or `expired`), so the Requests tab can
offer a connection that no longer works but still has rows.

## Upkeep

The `identity.api-keys` worker runs when `IDENTITY_API_KEYS_ENABLED`, `IDEMPOTENCY_ENABLED` or
`OAUTH_ENABLED` is on, and prunes, under a lease of its own and whatever `OAUTH_ENABLED` says:
codes an hour after they were spent or expired (so a replay still finds its connection), tokens a
day past their expiry, connections never activated 10 minutes after they were approved,
connections 90 days after they were revoked or expired, and cached documents a week past their
expiry.
