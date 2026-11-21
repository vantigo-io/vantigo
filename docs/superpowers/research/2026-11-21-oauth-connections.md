# OAuth connections for MCP clients and apps — research

Research, 2026-11-21, following API access (`specs/2026-11-17-api-access-design.md` §12 "OAuth 2.0
apps"), the MCP server (`specs/2026-11-19-mcp-server-design.md` M6, §8 "OAuth and ChatGPT") and the
Idempotency-Key phase. Code references are against `c2add972d`; `srv/` is `apps/server/internal/`.
"(unverified)" means the source did not confirm it.

## 1. Why now

`/api/mcp` takes a pasted `vtg_…` key in a header. That works for Claude Code and other clients that
are configured in a file. It does not work where the client only offers "Connect" and a sign-in:
**ChatGPT's connectors accept OAuth only**, and Claude's custom connectors on the web, desktop and
mobile start OAuth from a `401`. A pasted long-lived key is also the weakest part of the MCP story:
it is copied into client configuration and lives until it expires. OAuth replaces it with a consent
screen, a one-hour token and a list of connected apps an Owner can revoke.

## 2. What exists in our code

- **No authorization server.** Vantigo is an OIDC *client* (`srv/identity/oidc.go`, `go-oidc` and
  `x/oauth2`); it has no signing keys, no JWKS and no `/.well-known/` route. Any unknown `GET` answers
  the SPA's `index.html` with `200` (`srv/web/handler.go:17-46`), so `/.well-known/*` returns HTML today.
- **One installation is one customer.** The issuer can be `APP_URL` (plus `APP_BASE_PATH`); there is
  no tenant to pick.
- **The check order** (`srv/identity/access.go:159-220`) already branches on the bearer's shape:
  `Bearer vtg_…` goes to `checkAPIKey`. A second prefix is a second branch.
- **Keys belong to integrations, never to people** (migrations 00194, 00195). `checkAPIKey`
  (`access_apikey.go:83-167`) refuses Owner and System admin roles, `APINever` permissions and
  session-only operations, applies the integration's rate limit and writes `identity.api_requests`.
  The API-access research said an OAuth client "would act as an integration" (:279-282), and left
  personal tokens open because of the Owner-MFA rule and offboarding (:283-286).
- **MCP** (`srv/module/apimcp.go`): `presentsKey` requires `Bearer vtg_`; anything else gets `401
  api_key_required` with `WWW-Authenticate: Bearer realm="vantigo"` and no `resource_metadata`. The
  loopback call copies the caller's `Authorization` and runs `Check` again (`apimcp_tools.go:669-700`),
  so a new bearer kind works through it unchanged. `mcp_access` (`off`/`read`/`read_write`) is per
  integration.
- **The session cookie is `SameSite=Strict`** (`cookies.go:91-106`). A cross-site navigation from
  `claude.ai` to an authorize URL arrives **without** it. The OIDC client's own cookies are `Lax` for
  that reason (`oidc.go:47-57`).
- **Sign-in has no return-to** (`sign-in.tsx:59-78` always lands on `/`).
- **Building blocks.** `newToken`/`hashToken` (32 random bytes, SHA-256 stored, lookup by unique
  index), `secrets.Box` with bound sealing, `netguard.DialContext` for outbound fetches, the
  `identity.api-keys` worker for pruning, `writeAudit`, the full-page card pattern
  (`accept-invitation.tsx`) and `publicPaths`. `identity.api_requests` has **no column** naming how the
  key was presented (MCP "Later": "an MCP mark … needs a column").
- **No CORS anywhere**, and `/api/*` sits behind `http.NewCrossOriginProtection`
  (`srv/server/server.go:49-58`), which lets requests without `Origin`/`Sec-Fetch-Site` through.
- The latest migration on `main` is **00217**; the one open PR (#340) is documentation.

## 3. What the protocol asks for

MCP authorization, revision **2026-07-28**
(<https://modelcontextprotocol.io/specification/2026-07-28/basic/authorization>):

- The MCP server is an OAuth 2.1 **resource server**. It MUST publish RFC 9728 protected-resource
  metadata and point to it from the `401` (`WWW-Authenticate: Bearer resource_metadata="…"`), SHOULD
  name the scopes there, answers `403 insufficient_scope` for a token that lacks one, and MUST
  reject a token that was not issued for it (RFC 8707 `resource`, sent by the client on both the
  authorization and the token request). It MUST NOT pass a client's token on to another service.
- The **authorization server** publishes RFC 8414 metadata (or OIDC discovery), supports PKCE `S256`
  and says so in `code_challenge_methods_supported` (clients refuse to proceed otherwise),
  exact-matches redirect URIs (loopback port may vary), SHOULD return `iss` on the authorization
  response (RFC 9207) and MUST rotate refresh tokens for public clients.
- **Client registration**, in the client's order of preference: pre-registered, **Client ID Metadata
  Documents** (CIMD, `draft-ietf-oauth-client-id-metadata-document-03`), Dynamic Client Registration
  (RFC 7591). **DCR is marked deprecated** in this revision.
- **CIMD**: the `client_id` is an `https` URL the server fetches. The document must repeat the
  `client_id` and carry `client_name` and `redirect_uris`. The server MUST NOT follow redirects or
  fetch special-use addresses, SHOULD stop reading at about 5 KB, MAY cache (never an error), and
  SHOULD show the `client_id`'s host on the consent screen; a client whose redirects are loopback-only
  can be impersonated by any local program and deserves a warning.
- OAuth 2.1 (`draft-ietf-oauth-v2-1-17`) and RFC 9700: authorization codes are single use and live
  at most ten minutes; replaying one revokes what it issued; a reused refresh token revokes the grant;
  no implicit or password grant; token responses are `no-store`; no bearer in the query string.
- Token format is the server's choice. A server that is its own resource server gains nothing from
  JWTs and loses immediate revocation; clients must treat tokens as opaque.

## 4. What the clients do today

| Client | Registration | Redirect | Notes |
| --- | --- | --- | --- |
| Claude (web, desktop, mobile) | CIMD when the server advertises it and `none` as a token-endpoint method; else DCR; or a client id typed into the connector dialog | `https://claude.ai/api/mcp/auth_callback` | needs a `401`; uses the first `authorization_servers` entry; the metadata's `resource` must equal the URL the person typed; asks for `offline_access` when advertised; expects `invalid_grant` on a dead refresh token; 10 s limit on discovery and token calls |
| Claude Code | CIMD or DCR, or `--client-id` | `http://localhost:<port>/callback` and `127.0.0.1` | an unexpected issuer fails the sign-in |
| ChatGPT | CIMD preferred (`https://chatgpt.com/oauth/client.json`), DCR | `https://chatgpt.com/connector_platform_oauth_redirect` | requires `S256` advertised; sends `resource`; compares `iss` exactly; no API keys at all |
| Cursor | DCR, or a static client id | `http://localhost:8787/callback`, a web callback | CIMD not mentioned |
| VS Code | DCR or a configured client id | `http://127.0.0.1:33418`, `https://vscode.dev/redirect` | CIMD only in secondary sources (unverified) |

Sources: <https://claude.com/docs/connectors/building/authentication.md>,
<https://developers.openai.com/apps-sdk/build/auth>, <https://cursor.com/docs/context/mcp>,
<https://code.visualstudio.com/api/extension-guides/ai/mcp>.

## 5. What comparable products show

Linear: coarse read or read-write, refresh for 25 days. Notion: no scopes, the token acts as the
person; a list of clients with the members connected to each, and (Enterprise) an approved-clients
list. Stripe: refresh tokens die **14 days after the first authorization**, an "OAuth sessions" list
per member that an admin can revoke, a team-wide switch. Atlassian: an allowed-domains list for
clients. GitHub: an admin approves an app before it reaches private resources. Nobody offers
per-resource scopes on an MCP server; all show who connected what and let an admin end it.

## 6. Pitfalls that shape the design

- **Who the token acts as.** A token for a *person* brings every open question personal tokens had:
  an Owner's token would bypass the Owner-MFA rule, `APINever` exists because some operations must
  not be reachable without a session, and offboarding must reach the token. A token for an
  *integration* reuses the whole API-access model, but then somebody must be allowed to decide that
  a client may act as that integration.
- **Consent phishing.** With CIMD (and DCR) the client picks its own name and logo. The screen must
  show what the server verified (the `client_id`'s host, the redirect host), not only what the client
  claims, and must not be frameable.
- **The Strict cookie.** The authorize URL cannot be a server endpoint that reads the session: it
  must be a page that loads, then talks to the API same-origin.
- **Open redirect and return-to.** Sign-in gains a return-to; it must accept exactly one shape.
- **CIMD fetch is SSRF.** Through `netguard`, no redirects, size cap, time limit, cached.
- **DCR is an unauthenticated write** that anyone on the internet can call; the protocol has
  deprecated it, the two clients that matter most do CIMD, and a client id can be registered by hand.
- **`/.well-known/` under a base path.** RFC 8414 inserts `/.well-known/…` before the issuer's
  path, so an installation at `/vantigo` must answer at the host root, outside `StripBasePath`.
- **Discovery changes the `401`.** Adding `resource_metadata` makes every OAuth-capable client that
  is missing a key start a sign-in. That is the intent, and it reverses MCP M6.

## 7. Recommended shape (R)

Vantigo becomes a small OAuth 2.1 authorization server for its own API: authorization code with PKCE
and refresh tokens, nothing else. A **connection** is one client authorized by one person to act as
**one integration**; its tokens are a second way to present that integration, checked by the same
code path as a key (roles, `APINever`, rate limit, request log, `mcp_access`). Clients are identified
by a CIMD URL or by a client the Owner registered by hand. Tokens are opaque and stored hashed.
The consent screen is an SPA page; discovery documents and the token endpoint are plain handlers.

## 8. Questions for the owner, with recommended defaults

1. **Does a connected client act as an integration or as the person who connected it?**
   *Default:* an integration, chosen on the consent screen. Acting as a person is a later phase with
   its own answers on MFA, `APINever` and offboarding.
2. **Who may approve a connection?** *Default:* whoever may manage API access today (the Owner).
   A permission that lets others connect a client to a named integration can follow.
3. **Which clients may connect?** *Default:* any CIMD client and any registered by hand; no DCR.
   The consent screen shows the verified host; an optional allow-list of client hosts is a setting.
   Cursor and VS Code then need a hand-registered client id.
4. **Scopes.** *Default:* two, `read` and `write`, plus `offline_access`. They cap the connection;
   the integration's roles and `mcp_access` still decide. No per-module scopes.
5. **Lifetimes.** *Default:* access token 1 hour; refresh token rotated on use, dead after 30 days
   unused and 180 days after consent; authorization code 60 seconds.
6. **Is the token good for the REST API or only `/api/mcp`?** *Default:* both, one audience (the
   installation); the reference documents it, MCP is what the screens talk about.
7. **Where does an Owner see connections?** *Default:* a "Connected apps" section on the integration,
   with the client, who connected it, when it was last used, and Revoke; archiving the integration or
   removing the person's access ends them; every change is audited.
8. **Does the request log name the connection?** *Default:* yes, a nullable `connection_id` column.
9. **Kill switch.** *Default:* `OAUTH_ENABLED` (default true); off removes discovery, the endpoints
   and the token branch, and the `401` goes back to today's.
10. **Browser-only clients (CORS on the token endpoint).** *Default:* not in this phase.
