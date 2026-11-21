# OAuth connections for MCP clients and apps — design

Revision 2, 2026-11-21 (revision 1 followed by two adversarial reviews, one against the code and
one against the protocol and the clients' live metadata documents; their findings are folded in as
**§11, which wins where it differs from the text above**). Builds on API access phase 1 (`specs/2026-11-17-api-access-design.md`,
K1–K61), the MCP server (`specs/2026-11-19-mcp-server-design.md`, M1–M36) and the Idempotency-Key
(`specs/2026-11-19-idempotency-key-design.md`), and on the research
`research/2026-11-21-oauth-connections.md` ("R"). The owner's questions in R §8 take R's recommended
defaults here; §10 says what overturning each would change. Decisions are numbered C*n*. Code
references are against `c2add972d`. `srv/` is `apps/server/internal/`; `host/` is
`apps/host/frontend/src/`.

## 1. Purpose

A person adds `https://<workspace>/api/mcp` to Claude or ChatGPT, is sent to Vantigo, signs in, picks
the integration the client may act as, and approves. The client then holds a one-hour token it
renews on its own; an Owner sees the connection on the integration and can end it. No key is copied
anywhere. A connection grants nothing an API key of the same integration could not do.

| Delivery | What it brings | Depends on |
| --- | --- | --- |
| **A** | The authorization server end to end for clients identified by a metadata document: discovery, the consent page, the token endpoint, the token as a way to present an integration, the MCP `401`, the connections list and Revoke, the request log's connection, `OAUTH_ENABLED`; docs | — |
| **B** | Clients registered by hand (for Cursor, VS Code, in-house apps), the client-host allow-list, the revocation endpoint, the request log's filter, the MCP section's "Connect by signing in" instructions; docs; the landing page | A |

**Not in this phase:** tokens that act as a person; a permission that lets non-Owners connect;
Dynamic Client Registration; the client-credentials grant; OpenID Connect (no ID token, no
`userinfo`); JWT access tokens and JWKS; per-module scopes; CORS and browser-only clients;
`private_key_jwt`; logos on the consent page.

## 2. Decisions — the model

- **C1 — A connection acts as an integration.** `identity.oauth_connections` holds one client
  authorized by one person for one integration: `id`, `integration_id` (FK, `ON DELETE CASCADE`),
  `client_id text`, `client_kind` (`metadata`|`registered`), `client_name` and `client_host`
  (snapshots from consent), `redirect_uri`, `scopes text[]`, `authorized_by` (FK users, `ON DELETE
  SET NULL`), `created_at`, `activated_at`, `expires_at` (C17), `last_used_at`, `last_used_ip`,
  `revoked_at`, `revoked_by`, `revoke_reason`. Its tokens present the integration exactly as a key
  does (C19). People still hold no credential of their own.
- **C2 — Who may approve.** The session operations of §4 take the policy the integration operations
  take (`policy:Owner+OwnerManagement`, with the Owner-MFA rule). They are `x-vantigo-api: never`
  and `x-vantigo-assistant: never`. A signed-in person who is not an Owner sees who to ask (C13).
- **C3 — Scopes.** `read`, `write` and `offline_access`. `write` includes `read`. A request with no
  scope, or only `offline_access`, means `read`. An unknown scope is `invalid_scope`. The consent
  page may lower `write` to `read`, never raise. `offline_access` is accepted and has no effect: a
  refresh token is always issued (C16). Scopes cap the connection; the integration's roles and
  `mcp_access` still decide what a request may do.
- **C4 — One live connection per client, integration and person.** Activating a connection (C15)
  revokes an earlier live one with the same `client_id`, `integration_id` and `authorized_by`
  (`revoke_reason` `replaced`), so reconnecting does not pile up rows.
- **C5 — The resource.** The canonical resource is `<issuer>/api/mcp`. A `resource` parameter is
  optional; when present on the authorization or token request it must equal that URL (a trailing
  slash is tolerated), else `invalid_target`. A token is accepted wherever a key is: Vantigo is the
  only issuer and the only resource server, and MCP's loopback (M-series) presents the same token to
  the REST API. The reference says so.

## 3. Decisions — clients

- **C6 — Clients identified by a metadata document.** A `client_id` that is an `https` URL with a
  path other than `/`, no userinfo, no fragment, no dot segments, at most 512 characters, and a host
  that is not this installation's, names a document Vantigo fetches: `GET` with
  `Accept: application/json`, through `netguard.DialContext` (no private, loopback, link-local or
  special-use address), **no redirects followed**, 5 s in all, at most 16 KiB read, `200` and a JSON
  object only. The document is accepted when `client_id` equals the URL by string comparison;
  `client_name` is a non-empty string; `redirect_uris` is 1–20 strings each valid by C9;
  `token_endpoint_auth_method` is absent or `none`; `grant_types`, when present, includes
  `authorization_code`; `response_types`, when present, includes `code`; and it carries no
  `client_secret`. Anything else is a refused client (C12). Logos and other URIs in the document are
  never fetched or shown.
- **C7 — The cache.** `identity.oauth_client_documents(client_id PK, document jsonb, fetched_at,
  expires_at)`. A good document is kept for its `Cache-Control: max-age`, clamped to 5 minutes–24
  hours, one hour when absent. A failure is never stored; a `client_id` is fetched at most once per
  10 seconds (in process), and the fetch sits behind the session and the per-IP limit of C11, so
  an anonymous caller cannot make Vantigo fetch. The document is read at authorization only; the token
  endpoint works from the connection.
- **C8 — Clients registered by hand (delivery B).** `identity.oauth_clients(id, client_id` (`vtc_`
  + 24 base62 characters, unique)`, name, redirect_uris text[], created_by, created_at, version,
  archived_at)`: public clients with PKCE, no secret. Owner-only operations under
  `/api/v1/identity/oauth-clients` (list, create, update with `If-Match`, archive), all
  `x-vantigo-api: never`. Archiving a client revokes its connections (`client_archived`).
- **C9 — Redirect URIs.** A redirect URI is `https` with a host, or `http` with host `127.0.0.1`,
  `[::1]` or `localhost`; no fragment; at most 1024 characters. The request's `redirect_uri` is
  required and must equal a registered one **by string comparison**, except that for an `http`
  loopback URI the port is ignored. `localhost` and `127.0.0.1` are different hosts.
- **C10 — No Dynamic Client Registration.** No `registration_endpoint`. Claude and ChatGPT use the
  metadata document; a client that cannot is registered by hand (C8).
- **C11 — Which clients (delivery B).** Setting `oauth_client_hosts` on the identity settings: empty
  (default) admits any metadata-document client; otherwise the `client_id`'s host must equal an entry
  or be a subdomain of one. Registered clients are always admitted. Changing it is audited and does
  not end existing connections.

## 4. Decisions — the flow

- **C12 — Discovery.** The issuer is `APP_URL` with `APP_BASE_PATH`, no trailing slash. Plain
  handlers, registered ahead of the SPA and outside `StripBasePath` for the host-root forms, answer
  `application/json` with `Cache-Control: public, max-age=300`:
  - `/.well-known/oauth-protected-resource<base>/api/mcp` and `/.well-known/oauth-protected-resource`:
    `resource` (C5), `authorization_servers: [issuer]`, `scopes_supported: ["read","write"]`,
    `bearer_methods_supported: ["header"]`, `resource_name: "Vantigo"`.
  - `/.well-known/oauth-authorization-server<base>` and `<base>/.well-known/oauth-authorization-server`:
    `issuer`, `authorization_endpoint` `<issuer>/oauth/authorize`, `token_endpoint`
    `<issuer>/api/oauth/token`, `response_types_supported: ["code"]`, `grant_types_supported:
    ["authorization_code","refresh_token"]`, `code_challenge_methods_supported: ["S256"]`,
    `token_endpoint_auth_methods_supported: ["none"]`, `scopes_supported:
    ["read","write","offline_access"]`, `client_id_metadata_document_supported: true`,
    `authorization_response_iss_parameter_supported: true`, and from B `revocation_endpoint`.
  Any other path under `/.well-known/` answers a `404`, not the SPA.
- **C13 — The consent page.** `<issuer>/oauth/authorize` is an SPA route (`host/routes/oauth.authorize.tsx`),
  a centred card as `accept-invitation.tsx`. It needs a session; the session cookie is
  `SameSite=Strict` and does not arrive with the navigation from the client, but the page's own
  requests are same-origin and carry it. Not signed in: the page stores its own path and query in
  `sessionStorage` (`oauth.return`, with a timestamp) and goes to `/sign-in`; the app's landing after
  any sign-in (password, 2FA, passkey, OIDC, forced MFA enrolment) consumes the entry when it is
  younger than 10 minutes and its path is exactly `/oauth/authorize`, and navigates there. There is
  no return-to parameter in any URL.
  Signed in, the page posts the query's parameters to `POST /api/v1/identity/oauth/authorizations/preview`.
  The answer is one of: **`client`** refused (unknown or invalid `client_id`, a `redirect_uri` that
  does not match, the allow-list) — the page says so and offers nothing but Close, and never
  redirects; **`redirectTo`** — the request is wrong in a way the client is told about
  (`response_type` not `code`, no `code_challenge` or a method other than `S256`, `invalid_scope`,
  `invalid_target`, `state` over 512 characters), and the page navigates there; or the **display**:
  the client's name, the host of its `client_id` (or "registered here by …"), the redirect's host,
  whether the redirect is loopback, the scopes asked, and the integrations with `eligible` and a
  reason. An integration is eligible when it is active and its `mcp_access` is not `off`.
  The page shows the name the client claims next to what Vantigo verified (the two hosts), a
  warning for a loopback redirect ("an app on this computer; any program on it could be asking"),
  the integration picker, Read only / Read and write (capped by the scopes asked and the
  integration's `mcp_access`), that the connection acts with the integration's roles, lasts until
  revoked and at most 180 days, and Connect and Cancel. A person who is not an Owner sees who to ask
  and Cancel. `client_name` is shown as text, stripped of control and bidirectional characters, cut
  at 80 characters.
- **C14 — Approving.** `POST /api/v1/identity/oauth/authorizations` repeats every parameter with
  `integrationId`, `access` and `decision` (`approve`|`deny`); the server validates all of it again
  (the preview is not trusted) and answers `{redirectTo}`. Deny, or Cancel, redirects with
  `error=access_denied`. Approve creates the connection, not yet activated, and a code: 32 random
  bytes (`newToken`), stored hashed in `identity.oauth_codes(code_hash PK, connection_id FK CASCADE,
  code_challenge, expires_at, consumed_at)`, valid **60 seconds**. Every redirect carries `state`
  when one was sent and `iss`. The page is not frameable (the existing CSP's `frame-ancestors`
  covers the SPA; the plan verifies it). Audit `connection.authorized` is written at activation,
  with the client, its host, the scopes and the integration.
- **C15 — The token endpoint.** `POST <issuer>/api/oauth/token`, hand-registered beside `/api/mcp`,
  `application/x-www-form-urlencoded` only, body at most 16 KiB, anonymous, cookies ignored, per-IP
  limit `OAuthToken` 120 a minute. Answers are RFC 6749 JSON with `Cache-Control: no-store`; errors
  are `invalid_request`, `invalid_client`, `invalid_grant`, `invalid_scope`, `invalid_target`,
  `unsupported_grant_type`, never a problem document. `Authorization: Basic` or a `client_secret` is
  `invalid_client`.
  `grant_type=authorization_code`: `code`, `client_id`, `redirect_uri` and `code_verifier` (43–128
  characters) are required. The code is consumed atomically (`UPDATE … WHERE consumed_at IS NULL AND
  expires_at > now() RETURNING`); `client_id` and `redirect_uri` must equal the connection's and
  `BASE64URL(SHA256(code_verifier))` the stored challenge (constant time). Success activates the
  connection (C4) and issues tokens (C16). A code presented a second time **revokes the connection**
  (`code_replayed`) and answers `invalid_grant`.
- **C16 — Tokens.** Opaque: `vta_` (access) and `vtr_` (refresh), each the key's encoding (43
  base62 characters of 32 random bytes and a 6-character checksum, `apikey.go:9-21`), stored only
  as `hashToken` in `identity.oauth_tokens(token_hash PK, connection_id FK CASCADE, kind, created_at,
  expires_at, used_at)`. The response is `access_token`, `token_type: "Bearer"`, `expires_in: 3600`,
  `refresh_token`, `scope`. An access token lives **1 hour**.
- **C17 — Refresh.** `grant_type=refresh_token` with `refresh_token` and `client_id` (must equal the
  connection's); a `scope`, when sent, must be within the connection's and does not change it. Each
  use issues a new access and a new refresh token and marks the old refresh token used. A used
  refresh token presented again within **60 seconds** is answered with another new pair (a client
  that lost the response retries); after that it **revokes the connection** (`refresh_reused`). A
  refresh token dies 30 days after it was issued; the connection ends 180 days after consent
  (`expires_at`). Dead, used-up, revoked or unknown is `invalid_grant`.
- **C18 — Ending a connection.** `DELETE /api/v1/identity/integrations/{id}/connections/{connectionId}`
  (Owner; `revoked`); archiving the integration (`integration_archived`); the person who authorized
  it being disabled, deleted or no longer an Owner (`authorizer_removed`, written where those
  changes are made; the token check also refuses while that person is inactive, which covers SCIM
  deactivation); replacement (C4), replay (C15, C17), a client archived (C8); and from B the
  revocation endpoint `POST <issuer>/api/oauth/revoke` (RFC 7009: `token`, always `200`;
  `revoked_by_client`). A revoked connection's tokens stop at the next request. Each ending is
  audited as `connection.revoked` with its reason.

## 5. Decisions — presenting the token

- **C19 — The check.** `Access.Check` gains a branch for `Bearer vta_…` beside `vtg_` (a bad
  checksum is refused before any read). One query finds the token, its connection, the integration,
  its user and roles, and the authorizer's state. Then **the key's pipeline runs unchanged**
  (`access_apikey.go:83-167`): `APIRefused` → 403 `api_not_available`; Owner and System admin roles
  refused; `APINever`; session-only operations 403; `evaluate` for permission rules. A connection
  has no network list and no permission narrowing. A connection without `write` is a read-only key
  (K11), answered 403 `insufficient_scope`.
- **C20 — The principal.** `Via.Kind` stays `api_key` — it means "an integration presenting itself",
  and everything keyed on it (the gate, the rate limit, the Idempotency-Key's scope, MCP) applies
  alike — with `Via.ConnectionID` set and `Via.KeyID` zero. `RateLimitPerMinute` is the
  integration's, shared with its keys. `last_used_at` is touched at most once a minute.
- **C21 — Refusals.** An unknown, expired or revoked token: `401` with `WWW-Authenticate: Bearer
  error="invalid_token", resource_metadata="<the C12 URL>"`. A scope refusal: `403` with `Bearer
  error="insufficient_scope", scope="read write", resource_metadata=…`. Neither clears a session
  cookie.
- **C22 — MCP.** `/api/mcp` accepts `vtg_` and `vta_`. With no bearer of either kind, the `401`
  becomes `WWW-Authenticate: Bearer realm="vantigo", resource_metadata="…", scope="read write"`
  (this replaces M6's plain challenge: an OAuth-capable client without a key now starts a sign-in).
  The effective mode is the narrowest of the integration's `mcp_access` and the connection's scope;
  `off` stays 403 `mcp_not_enabled`. The four-in-flight limit counts per connection.
- **C23 — The request log.** `identity.api_requests.key_id` becomes nullable and
  `connection_id uuid` is added, with a check that exactly one is set. The Requests tab names the
  connection's client where it named the key; a filter by connection comes with B.
- **C24 — The kill switch.** `OAUTH_ENABLED` (default true) in `srv/config`. False: the discovery
  paths and the token and revocation endpoints answer `404`, the session operations `404`, a `vta_`
  token `401`, and the MCP `401` is M6's. Connections are kept.

## 6. Decisions — the screens, upkeep, docs

- **C25 — Connected apps.** `GET /api/v1/identity/integrations/{id}/connections` lists activated,
  unrevoked, unexpired connections: client name and host, kind, who connected it, scopes, connected,
  last used, ends. The integration's Overview gains "Connected apps" under MCP, with Revoke behind
  a confirmation. From B the MCP section shows the URL to paste into Claude or ChatGPT and says a
  sign-in follows; a "Registered clients" section sits on the API access page.
- **C26 — Upkeep.** The `identity.api-keys` worker deletes consumed or expired codes, tokens a day
  past expiry, connections never activated after 10 minutes, connections 90 days after they were
  revoked or expired, and cached documents a week past expiry. It runs when `OAUTH_ENABLED` is on,
  as it does for the Idempotency-Key.
- **C27 — Languages.** The consent page and the sections are in English and Norwegian bokmål
  (`host/catalogs/api-access.ts`, `auth.ts`).
- **C28 — Documentation.** A: new `reference/oauth.md` (the endpoints, discovery, client rules,
  lifetimes, errors, what a connection may do); `reference/mcp.md` (authentication, the `401`);
  `reference/api-introduction.md` (a token beside a key); `admin/api-access.md` in `en` and `nb`
  (connecting Claude and ChatGPT, Connected apps, ending one, `OAUTH_ENABLED`);
  `admin/authentication.md` in both (sign-in returning to a connection) and
  `contributing/module-boundaries.md` if the router or server mux pages' sources change. B: the
  same pages for registered clients, the allow-list and revocation. The landing site's API line
  after B.

## 7. Edge cases

- **A client whose document declares `private_key_jwt`** is refused (C6). The builder of A fetches
  Claude's and ChatGPT's published documents and records what they declare in the plan's notes; if
  either cannot connect as a public client, that is an amendment before A merges.
- **A base path.** Every URL in discovery carries it; the host-root well-known forms insert it.
- **`APP_URL` is `http`** (development): the issuer is `http`; clients that demand `https` will
  not connect. Not worked around.
- **The integration's MCP is switched off after connecting:** the connection stays, `/api/mcp`
  answers 403, REST still works.
- **The integration's roles change:** the next request sees them, as for a key.
- **Two tabs approve the same request:** two connections, the second activation replaces the first.
- **A person signs in as someone else mid-flow:** the preview and the approval both run as the
  current session; nothing from before sign-in is trusted but the query string.
- **A client sends `resource` only on one of the two requests:** accepted (C5 checks what is sent).
- **Clock skew:** all expiry is the database's clock.

## 8. Testing

Unit: client-id and redirect validation tables (loopback ports, `localhost` vs `127.0.0.1`, dot
segments, userinfo, own host); the document rules against a fake server (redirect, oversized,
non-200, mismatch, private address); PKCE; the cache clamp. Integration (`srv/integration`): the
whole flow against a fake metadata host — preview, approve, exchange, call `/api/mcp` and a REST
operation, refresh, the 60-second grace, reuse after it, code replay, revoke, archive, authorizer
disabled; every refusal's status, body and `WWW-Authenticate`; discovery bodies with and without a
base path; `OAUTH_ENABLED=false`; the request log row; a pool of one. Frontend: the consent page's
states, the sign-in return, Connected apps. The whole-contract tests (`APINever`, the
Idempotency-Key's exclusion list) are kept green for the new operations.

## 9. Later

Acting as a person; a "may connect" permission; `private_key_jwt`; CORS and browser clients; a mail
to the Owners when a client connects; per-module scopes; step-up scope challenges; OpenID Connect;
an approved-clients mode that asks an Owner to approve a new client once.

## 10. What overturning a default changes

1. **Act as the person (R Q1):** C1, C2, C19 and C20 change: a person-bound principal, the
   Owner-MFA rule and `APINever` need their own answers; the largest change, a phase of its own.
2. **Others may approve (Q2):** a permission and a per-integration list of who may connect to it;
   C2, C13, C18's authorizer rule.
3. **DCR (Q3):** a registration endpoint, its limits and clean-up; C10.
4. **Scopes (Q4):** C3 and the consent page.
5. **Lifetimes (Q5):** constants in C14, C16, C17.
6. **MCP only (Q6):** C19 refuses a `vta_` token outside `/api/mcp` and its loopback, which then
   needs a mark on the looped request.
7. **Connections outlive the person (Q7):** drop the authorizer rule in C18 and C19.
8. **No connection in the log (Q8):** C23 shrinks to a nullable `key_id`.
9. **Off by default (Q9):** the default in C24.
10. **Browser clients (Q10):** CORS on discovery, token and MCP, and an Origin policy.

## 11. Amendments (revision 2, from the reviews)

**Clients**

- **A1 (C6) — ChatGPT's document.** Fetched 2026-11-21: `https://claude.ai/oauth/claude-code-client-metadata`
  and `https://claude.ai/oauth/mcp-oauth-client-metadata` declare `none`;
  `https://chatgpt.com/oauth/client.json` declares `token_endpoint_auth_method: private_key_jwt`
  **and** `token_endpoint_auth_methods_supported: ["none","private_key_jwt"]`. The rule becomes: when
  `token_endpoint_auth_methods_supported` is present it must include `none` and the singular field is
  ignored; otherwise the singular must be absent or `none`. A token request with `client_assertion`,
  `client_secret` or `Authorization: Basic` is `invalid_client`. The three documents are test fixtures.
- **A2 (C6) — The fetch.** Host in ASCII only (a non-ASCII or punycode-decoding-differently host is
  refused), port 443 only, `Transport.Proxy: nil`, `CheckRedirect: http.ErrUseLastResponse`, a fixed
  `User-Agent: Vantigo`, the 16 KiB cap counted after decompression and reaching it an error. The
  fetch goes through a seam `oauthNet` (as `webhookNet`, `access.go:70-93`) so tests reach a fake
  host with their own resolver and roots. Only an Owner who meets the MFA rule triggers a fetch;
  previews are limited to 20 a minute per person. `admin/api-access.md` says outbound HTTPS to the
  client's host is needed.
- **A3 (C7) — Revalidation (delivery B).** At a refresh whose cached document has expired, the
  document is fetched again in the background; a terminal failure (`client_id` mismatch, the
  redirect URI gone, a rule of C6 broken) revokes the connection (`client_document_changed`); a
  transient one changes nothing.
- **A4 (C9) — Comparing redirects.** Both URIs are parsed. Non-loopback: string equality. Loopback
  `http`: scheme, host, path and query equal, port ignored; a registered URI without a port matches
  any. `code`, `state` and `iss` are added to an existing query, not appended as text.

**The flow**

- **A5 (C4) — No replacement.** Activating a connection never revokes another: every Claude Code
  install shares one `client_id`, and a second laptop must not end the first. C4 is withdrawn; the
  list shows each connection with its date and last use.
- **A6 (C13) — Nothing redirects before the client is known.** `client_id` and `redirect_uri` are
  validated first. An error is redirected automatically only when the redirect's host equals the
  `client_id`'s host or is loopback (and always for a registered client); otherwise the page shows
  the error and a "Return to <host>" button. A person who is not an Owner sees "Ask an Owner of this
  workspace" and Close: no fetch, no integrations, no redirect. An Owner who has not enrolled MFA is
  sent to enrol and returns (A8).
- **A7 (C13) — What the page says.** Hosts are shown in ASCII. The claimed name is labelled as a
  claim ("calls itself …") beside the host Vantigo fetched the document from; a name containing
  "Vantigo" or the workspace's name gets a warning. For a loopback redirect the page does not say
  "verified": "document hosted at <host>; any program on this computer can use it". No integration
  is preselected; access defaults to Read only. A client host nobody has connected before gets a
  first-use notice. The allow-list stays in B.
- **A8 (C13) — The return.** `/oauth/authorize` joins `publicPaths` (`host/lib/public-paths.ts`), so
  the root's `beforeLoad` does not redirect it away before it can act. The page reads the session
  itself; without one it stores `oauth.return` and goes to `/sign-in`. The root, after its session
  and MFA-enrolment gates pass (`__root.tsx` after the enrolment check), consumes a valid entry and
  navigates there. That one place serves password, 2FA, passkey, OIDC (a server redirect to `/`) and
  forced enrolment.
- **A9 (C14, C15) — Hygiene.** The approve response is `Cache-Control: no-store`. A repeated
  parameter is `invalid_request` at both endpoints; every parameter has a length cap.
  `code_challenge` is exactly 43 characters of `[A-Za-z0-9_-]`; `code_verifier` matches
  `[A-Za-z0-9._~-]{43,128}`. `invalid_client` answers 401, other token errors 400. The token and
  revocation endpoints are mounted **ahead of** `CrossOriginProtection` (as `/api/v1/live`,
  `server.go:93-114`): they read no cookie, and a client that sends `Origin` must get RFC 6749 JSON.
- **A10 (C16) — A refresh token is not an access token.** The stored hash covers the random bytes
  only, so `vtr_X` rewritten as `vta_X` would match. Every lookup filters `kind`: the check reads
  `access` only, the token endpoint `refresh` only.
- **A11 (C17) — One live refresh token.** `oauth_tokens` gains `parent_hash`. A refresh is `UPDATE …
  SET used_at = now() WHERE token_hash = $1 AND kind = 'refresh' AND used_at IS NULL RETURNING`. A
  used token presented within 60 seconds is honoured **once more**, and issuing then marks every
  other unused refresh token of the connection used, so a connection never has two live ones; a
  token superseded that way and presented later is `invalid_grant` without revoking; a used token
  presented after the 60 seconds revokes the connection. Every token expires at the earlier of its
  own lifetime and the connection's `expires_at`, and `expires_in` says so.
- **A12 (C5) — Comparing `resource`.** Scheme and host are lower-cased and a default port dropped
  before comparing; `<issuer>` itself is also accepted. The issuer and the resource are built from
  `APP_URL` normalised the same way, and the screens show that exact string.
- **A13 (C12) — Discovery.** Registered in `server.go`'s `root`, before the mux and the SPA. The
  `resource_metadata` of C21 and C22 is the path-suffixed URL. Authorization-server metadata adds
  `response_modes_supported: ["query"]`, and from B `revocation_endpoint_auth_methods_supported:
  ["none"]`. With a base path the host-root forms lie outside it: `admin/installation.md` (both
  languages) tells the operator to route `/.well-known/oauth-*` to Vantigo. With `MCP_ENABLED`
  false the protected-resource documents answer 404; the rest stays.
- **A14 (C22) — ChatGPT.** Each MCP tool's descriptor carries `securitySchemes: [{type: "oauth2",
  scopes: […]}]` (`read` for the reading tools, `write` for `write` and `delete`).

**Presenting the token**

- **A15 (C19, C20) — Not "unchanged".** The key's code reads the key row and refuses a zero `KeyID`
  (`access_apikey.go:220`, `apimcp.go:163`, `apirequests.go:127`). The mapping:

  | Key | Connection |
  | --- | --- |
  | `allowed_networks`, `permissions` | none |
  | `revoked_at` | the connection's |
  | integration archived, its user disabled or locked | the same |
  | `expires_at` | the token's and the connection's |
  | `read_only` | `write` not in scopes |
  | `TouchAPIKey` | `TouchConnection`, once a minute |
  | — | the authorizer (A17) |

  `Check` dispatches `vta_` before the session branch (today a `vta_` bearer would fall through to
  the cookie). `MCP()` and `Scope()` branch on `Via.ConnectionID`: mode from the integration,
  read-only from the scope, permissions the effective set less `APINever`. `Reject` treats `vta_` as
  it treats a key (no cookie cleared) through a `rejectConnection`; an unresolved token is a
  sentinel `ErrInvalidToken` that `/api/mcp` answers with C21's 401, not `api_key_required`. Body
  codes: `token_invalid` (401), `insufficient_scope` (403). `presentsKey` accepts both prefixes; the
  in-flight limiter is keyed by the key **or** the connection.
- **A16 (C21) — When `insufficient_scope`.** Only when the connection's own scope is the sole
  reason (no `write`). A refusal the integration's `mcp_access` or roles cause is the plain 403 a
  key gets, with no Bearer challenge, so a client does not loop on a step-up it cannot win.
- **A17 (C18) — The authorizer.** The token check requires `authorized_by` to be set and that person
  to be a person, not disabled, not pending, **an Owner now**, and not deactivated upstream while SCIM
  is on (no Owner exemption here); a password lockout is ignored, or anyone could end an Owner's
  connections by failing their password. That predicate is the rule. Revocation rows
  (`authorizer_removed`) are written for the audit where it is cheap and certain: user delete, in
  the same transaction before `SET NULL` (`users.go`, `provisioning.go`); disable; and the Owner
  role's removal in `authorization.go`. A connection whose authorizer no longer qualifies by another
  route (a group mapping, provisioning) is listed as **inactive** with the reason, and can be revoked.
- **A18 (C23) — The log.** No `CHECK` (it would scan 30 days of partitions under an exclusive lock
  and stall the writer); the writer sets exactly one. `contracts.APIRequest` and `APIKeyRefusal`
  gain `ConnectionID`; both ids are nullable parameters and `uuid.Nil` is written as `NULL`.
  `IntegrationRequest.keyId` becomes nullable with `connectionId` and `clientName` beside it; the
  list query joins connections; the Requests tab names the client. New reasons fit `varchar(20)`:
  `insufficient_scope`, `token_invalid`. B adds an index on `(connection_id, at)` with its filter.
- **A19 (C20) — The Idempotency-Key** is scoped to the integration, shared by its keys and
  connections; two sessions that pick the same key for different requests get the 422. Stated in
  the reference.

**Upkeep and structure**

- **A20 (C1) — Foreign keys.** `integration_id` is `ON DELETE RESTRICT` as keys are (`00195`);
  archiving revokes in the transaction that revokes the keys. `revoked_by` is `ON DELETE SET NULL`.
- **A21 (C26) — The worker** runs when any of its three switches is on and prunes OAuth rows
  whatever `OAUTH_ENABLED` says, under its own advisory lease. It polls hourly: consumed codes are
  kept **one hour** (so a replay still finds its connection), unactivated connections go at the
  first run after 10 minutes.
- **A22 — Seams and registries.** `srv/server` and `srv/module` may not import identity: the token
  and revocation handlers are reached through `contracts.OAuthEndpoints`, found by type assertion on
  `Access` (as `APIRequestRecorder`); discovery needs `Config` only. A builder also touches the
  integration test's identity operation map (`assistant_test.go`), `idempotencyExcluded`, the
  reason enum in `openapi/identity.yaml`, `sources` frontmatter for new paths, and runs `go generate`
  after docs edits. With `CSP_REPORT_ONLY`, framing is refused by `X-Frame-Options` alone.
- **A23 — Delivery B's revocation endpoint** requires `client_id` and revokes only when it is the
  token's connection's; limited like the token endpoint.
- **A24 (§1) — Three deliveries.** §1's A is split so that no half of the flow is ever advertised:
  **A** is the server (tables, clients by metadata document, discovery, the session operations, the
  token endpoint, the token check, MCP, the log, the list and Revoke operations, upkeep) with
  `OAUTH_ENABLED` **defaulting to false**; **B** is the consent page, the sign-in return, Connected
  apps and the Requests tab, turns the default to **true**, and carries the administration guide in
  both languages; **C** is §1's B (registered clients, the allow-list, revalidation, the revocation
  endpoint, the log's filter, the MCP section's instructions) and the landing page.
