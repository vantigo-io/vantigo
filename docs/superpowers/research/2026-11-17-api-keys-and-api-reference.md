# API keys and the API reference at /api: research

On 2026-11-17 the owner proposed the next feature. Administrators generate **API keys
with scoped access**, so external applications can use the API Vantigo already has. An
interactive reference (Scalar or similar) is served at **`/api`**. The owner also asked
for the options around this. This note covers four things: what the codebase already
has, what comparable products do, a recommended shape, and the questions only the owner
can answer. A design spec would start from §7.

Code references are against `a9f140c82`, whose latest migration is `00192`. `srv/` means
`apps/server/internal/`.

## 1. Where the codebase stands

### 1.1 Authentication

- **No middleware loads the caller.** Each operation is wrapped by the module router
  (`srv/module/router.go:313-353`). The order is: rate limit, then `Access.Check(req,
  rule)`, then the handler with a `contracts.Principal` on the context.
- **`identity.Access.Check`** (`srv/identity/access.go:108-153`) resolves in this order:
  1. an in-process grant (assistant or agent), which never comes from a header;
  2. `RuleSCIM`, using `Authorization: Bearer` against the SCIM digest;
  3. a station (`Authorization: Station` plus the station cookie);
  4. the `vantigo.session` cookie.

  API keys become a fifth branch.
- **A shared helper mints the secrets** (`srv/identity/tokens.go`): 32 random bytes,
  base64url, stored as SHA-256. Sessions, invitations and resets use it. A key can reuse
  it.
- **CSRF.** CSRF protection wraps `/api` (`srv/server/server.go:50-62`, Go's
  `CrossOriginProtection`). A request without `Sec-Fetch-Site` or `Origin`, which is any
  server-side client, passes. There is **no CORS**, so browser apps on other origins
  cannot call the API. That suits server-to-server keys.

### 1.2 Authorization is already declarative, per operation

- Every operation carries `x-vantigo-access` (1,363 occurrences in `openapi/*.yaml`).
  The kinds are:

  | Kind | Operations |
  |---|---|
  | `permission:…` | ~1,166 |
  | `policy:…` | 114 |
  | `anonymous` | 35 |
  | `session` | 34 |
  | `scim` | 14 |

  The parser is `srv/contracts/permission.go:59-79`. The lint is
  `srv/openapi/lint.go:13`.
- Permission keys are `module:verb`. There are about 130 in the catalog, and about 39
  are marked `Sensitive` (for example `customers:personal-data` and `accounting:post`).
  Each module declares its own, for example `srv/customers/module.go:40-56`.
- **Precedents for a non-human principal:**
  - **Agents (virtual employees)** are `identity.users` rows with `kind='agent'`
    (`00141`). They hold roles, have no credential, and are refused Owner and
    SystemAdmin.
  - An agent grant meets only permission rules, anonymous rules, and session rules
    marked `x-vantigo-agent: allowed`. It never meets SCIM or a policy
    (`srv/identity/agent_grants.go:34-89`).
  - The principal carries `Via{Kind:"agent",…}`.

  An API key is the same shape with a credential added.
- **Owner MFA.** When `OwnersRequireMFA` is set, Owners need a session's MFA even on
  plain permission rules (`access.go:535-544`). A key backed by an Owner would fail, or
  would bypass MFA. Either way, keys must not be backed by people.

### 1.3 The contract at runtime

- The specs are embedded (`srv/openapi/openapi.go:28-37`).
- `GET /api/openapi.json` serves the merged contract of the enabled modules, behind
  `RuleSession`, so cookie only (`srv/module/compose.go:663-676`).
- The merge emits **no `securitySchemes`**, and no contract declares one.
- **`/api` exactly** is registered as the 404 problem (`compose.go:682-683`). Serving a
  page there means changing that one line, and giving the page its own CSP: the app's
  CSP allows script hashes only (`srv/security/headers.go:33-49`). The docs handler
  already sets its own CSP (`srv/docs/handler.go:25-39`).
- **An earlier decision is on record.** The Go port design said a Scalar page would live
  at `/api/docs` and be *served behind an authenticated session only — never
  anonymously* (`specs/2026-09-10-go-backend-port-design.md:225-227`). The identity
  design deferred it (`specs/2026-09-11-identity-design.md:102, 310`). Nothing was
  built.
- **docs.vantigo.io already renders the API reference** from `openapi/*.yaml` with
  `starlight-openapi` (`docs/astro.config.mjs:14-36, 99-105`). Two gaps:
  - Agents and Meta are left out of that list.
  - The build embedded in the binary drops the reference (`DOCS_EMBED=1`, ~74 MB).

  So an installation has no API reference of its own today.

### 1.4 What an external caller would run into

- **Attribution.** About 63 call sites read `UserID(ctx)` or a `callerID(ctx)` helper
  for `created_by` and actor columns. A principal with no user row would break them or
  write `uuid.Nil`. A user row of a new kind fits the existing code.
- **Session-only handlers.** Some handlers need a `SessionID`: identity's `callerFrom`
  (`srv/identity/server.go:104-110`) and Google's personal connect
  (`srv/google/personal_connect.go:76`). These, and the `session` and `ActiveAccount`
  operations (account, MFA, passkeys), are personal self-service. Keys should never
  reach them, which is the agents' rule again.
- **Rate limits.** They run **before** auth, keyed by client IP, and only on operations
  a module lists (`router.go:315-325`). Nothing limits per credential.
- **Audit.** Identity's audit trail (`srv/identity/audit.go`) covers authorization
  changes only. There is no per-request log.
- **Events.** There are about 37 named domain events on a transactional outbox
  (`srv/events`, `platform.events` / `event_deliveries`). They reach only in-process
  handlers. There are **no outgoing webhooks**. Every "webhook" in the code is inbound
  (Meta, Vipps, Storecove, Entra, Google).

## 2. What comparable products do

| Product | Credential | Scope | Notes |
| --- | --- | --- | --- |
| **Tripletex** | Consumer token (per integration, approved by Tripletex) plus employee token (per company), exchanged for a session token | Employee token's **entitlements, chosen per module** at creation | Only administrators create employee tokens ([auth docs](https://developer.tripletex.no/docs/documentation/authentication-and-tokens)) |
| **Fiken** | OAuth2 apps; API is a paid add-on (99 NOK/month) | OAuth scopes | Apps limited to 5 users until approved for production ([Chift guide](https://docs.chift.eu/connectors/accounting/fiken)) |
| **PowerOffice Go** | OAuth2 client credentials, application key plus client key | Per client | ([PowerOffice auth](https://developer.poweroffice.net/documentation/authentication)) |
| **Odoo 18** | API key per **user**, shown once, optional duration | **None**: the key acts as the user | ([Cybrosys guide](https://www.cybrosys.com/blog/how-to-connect-with-odoo-18-using-an-api-key)) |
| **GitHub / Cloudflare / Vercel** | Prefixed tokens with a checksum | Fine-grained permissions per token | Prefixes make leaked keys findable by secret scanners ([Cloudflare token formats](https://developers.cloudflare.com/fundamentals/api/get-started/token-formats), [Vercel](https://vercel.com/changelog/new-token-formats-and-secret-scanning)) |

What these products share:

- Keys are made by an administrator.
- A key is scoped to **chosen modules or permissions**, not to "whatever the person
  could do".
- The secret is shown once and stored hashed.
- Expiry is optional or enforced.
- Third-party apps sold to many companies get **OAuth**. A company's own integration
  gets a **key**.

Odoo's user-bound keys are the counterexample: when the person leaves, their
integration breaks.

### API reference renderers

- **Scalar.** MIT licensed and self-hostable as one standalone JS bundle. It has a
  built-in client ("try it") with bearer auth and supports OpenAPI 3.1
  ([Scalar](https://scalar.com/resources/compare/readme),
  [comparison](https://www.pkgpulse.com/guides/scalar-vs-redoc-vs-swagger-ui-api-documentation-2026)).
- **Redoc.** Very readable, but has no client.
- **Swagger UI.** The incumbent. It looks dated and its OpenAPI 3.1 support is partial.
- **Stoplight Elements.** Made to embed inside another docs site, and its maintenance
  has slowed.

**Scalar fits best:** the contracts are OpenAPI 3.x, and an integrator holding a fresh
key wants to try a call at once. Bundle it into the binary rather than load it from a
CDN, for three reasons: the CSP, offline installations, and pinning one version.

## 3. Recommended shape

### 3.1 Integrations own keys; people don't

- An **integration** is an `identity.users` row with a new `kind='integration'`. It
  extends the `00141` CHECK and keeps its "no password, no TOTP" rule. It has:
  - a name, a description and an owner (the person responsible);
  - the **roles** it holds, so its scope uses the roles and permissions machinery that
    already exists;
  - one or more **keys**.
- Each **key** can optionally be **narrowed** further to a subset of the integration's
  permissions, or to read-only (the `:view` keys).
- What a key can do is **its integration's roles ∩ the key's narrowing**.
- Keys survive staff turnover, attribution writes a real user UUID, and the agents'
  shape is reused almost as is.

### 3.2 Which operations a key can reach

The rule, mirroring `agent_grants.go`:

- A key meets **permission rules** and **anonymous rules** only.
- A key never meets `session`, `policy:*` (Owner, OwnerManagement,
  AuthorizationManagement, SystemAdmin, ActiveAccount) or `scim` rules.
- Consequence: a key can never manage users, roles, SSO or other keys, and the Owner
  MFA question disappears.
- Add a contract flag **`x-vantigo-api: never`**, linted like `x-vantigo-assistant`,
  for permission operations that must not be exposed. Examples: personal-connect OAuth
  starts, and operations that need a `SessionID`.
- The merged spec at `/api` then lists **exactly what a key can call**. That list is
  also the public surface the stability promise (§3.6) covers.

### 3.3 The credential

- **Format:** `vtg_` + 32 random bytes in base62 + a CRC32 checksum. The prefix lets
  GitHub's secret scanning and tools like gitleaks spot a leaked key. The checksum lets
  them reject look-alikes offline.
- **Storage:** stored as SHA-256 (`tokens.go`). Only the prefix and last four
  characters are kept for display. The secret is shown **once**.
- **Header:** `Authorization: Bearer vtg_…`.
  - `Check` tries the key branch only for a `vtg_` bearer, and only on rules other than
    SCIM, so SCIM's bearer is untouched.
  - `Reject` must not clear a session cookie for a key request, as it already skips
    this for stations.
- **Expiry:** required. Default 90 days, maximum 1 year (see Q3).
- **Rotation:** "roll" issues a new key and keeps the old one alive for a chosen
  overlap (up to 7 days).
- **Tracking:** last used at and last IP, updated at most once a minute per key so the
  hot path never writes on every call.
- **Revocation:** immediate. Revoking a key, or disabling its integration, takes effect
  on the next request.
- **Optional IP allowlist** per key (CIDRs).

### 3.4 Who manages them, and the trail

- **Screen.** A *Workspace admin › Integrations › API keys* screen, beside the existing
  Integrations section (`specs/2026-10-31-integrations-and-configuration-tiers-design.md`).
- **Who.** Creating, rolling and revoking requires the **OwnerManagement** policy with
  MFA (see Q1). A key can carry Sensitive permissions, so the screen marks them as it
  does for roles.
- **Audit.** Identity's audit trail records `integration.created`, `key.created`,
  `key.rolled`, `key.revoked`, `key.expired` and role changes.
- **Request log.** Phase 1 records per-key request counts (method, operationId, status)
  in a rolling **request log** kept for 30 days. Admins can see what an integration is
  doing, which is the first question after a leak.
- **Expiry notices.** Mail and a notification go to the integration's owner 14 and 3
  days before a key expires.

### 3.5 Rate limiting per key

- Today's limits run before auth and are keyed by IP. Key requests need a **per-key
  bucket**. For example, a default of 600 requests per minute per key, configurable per
  integration, enforced after the key resolves.
- Responses carry standard `RateLimit-*` headers, and `429` comes with `Retry-After`.
- This extends `srv/ratelimit` (fixed windows in Postgres). An in-memory token bucket
  per instance would be cheaper but wrong across replicas. Read the hot-path cost
  before choosing.

### 3.6 The API becomes a promise

Until now the API was the frontend's. Once keys exist, integrators build against it.
That needs:

- a written **compatibility policy**: what is additive within `/api/v1`, how
  deprecation is announced (`Deprecation`/`Sunset` headers), and how long `v1` lives;
- a **breaking-change check in CI**, for example `oasdiff breaking` of `openapi/*.yaml`
  against `main`, limited to operations not marked `x-vantigo-api: never`;
- **`Idempotency-Key`** on unsafe operations that create things (invoices, orders,
  payments), so a retrying client never double-posts. This can come in a later layer,
  but it should be designed in now.

### 3.7 The reference at `/api`

- **`GET /api` with `Accept: text/html`** serves the Scalar page. Any other `Accept`
  keeps today's 404 problem, so the API's "unknown path → problem JSON" contract holds.
  The page carries its own CSP, as `/docs` does.
- **Who may see it.** The page shell is static and public. Like `/docs`, it reveals
  nothing.
- **The spec it loads.** `/api/openapi.json` gains one rule: session **or** API key.
  - A signed-in admin sees the whole API of the enabled modules.
  - An integrator who pastes a key into Scalar's auth box sees, and can try, **only the
    operations that key reaches**. The spec is filtered by the key's permissions.
  - This keeps the earlier "never anonymously" decision for the spec itself, where the
    real information is.
- **The merged spec gains:**
  - a `bearerAuth` security scheme;
  - per operation, the permission it needs, rendered from `x-vantigo-access` into the
    description;
  - `servers` set to the installation's `APP_URL`.
- **docs.vantigo.io keeps its static reference** for people evaluating Vantigo.
  Separately, Agents and Meta should be added to its contract list.
- A **"Download OpenAPI"** link lets integrators generate clients with their own tools
  (openapi-generator, oapi-codegen, openapi-typescript).

## 4. The further opportunities, ranked

1. **Outgoing webhooks.** This is the largest win after keys.
   - The outbox already names about 37 domain events (`orders.sales_order.confirmed`,
     `invoices.payment.received`, `purchasing.receipt.posted`, …).
   - An integration subscribes to events with an HTTPS URL. Each delivery is signed
     (HMAC-SHA256, [Standard Webhooks](https://www.standardwebhooks.com/) headers) and
     retried with backoff from `event_deliveries`.
   - The admin screen shows the delivery log and has "resend".
   - The payload is a thin event (id, type, resource URL), and the receiver fetches the
     resource with its key. The permission check then stays in one place.
   - Webhooks turn polling integrations into reactive ones and unlock Zapier, Make and
     n8n.
2. **An MCP server at `/api/mcp`.**
   - The Assistant already has an operations dispatcher with per-operation
     `x-vantigo-assistant: auto|confirm|never` flags (`srv/operations`).
   - Exposed over the Model Context Protocol and authenticated by an API key, it lets
     Claude, ChatGPT, Copilot and in-house agents work with Vantigo under the same
     scopes.
   - It would be a strong selling point, and most of the machinery exists.
3. **OAuth 2.0 for third-party apps** (authorization code + PKCE, plus client
   credentials). This is needed only when outside vendors build apps for many Vantigo
   customers: a marketplace, as Fiken and Tripletex have. It is a later phase, and the
   integration-account model above is what an OAuth client would act as.
4. **Personal access tokens** (a person's own key, acting as them). These are useful for
   scripts and personal MCP use. They raise the MFA and offboarding questions that
   integration keys avoid. Not in phase 1 (see Q5).
5. **Official SDKs and a Postman/Bruno collection**, generated from the merged spec in
   CI. They are cheap once §3.6's stability exists.
6. **Usage dashboard per integration**: calls, errors and the slowest operations, from
   the request log.
7. **Browser access (CORS) per integration**, with an origin allowlist. This is **not
   recommended**: a key in a browser is a leaked key. OAuth with PKCE is the right
   answer for browser apps.
8. **Bulk endpoints and CSV/JSON import** through the API, for migrations from other
   systems. These would be driven by real integrator demand.

## 5. Phases

| Phase | Scope | Pages (en + nb where bilingual) |
| --- | --- | --- |
| **A: integrations and keys** | `kind='integration'`, keys, the `Check` branch, `x-vantigo-api` and its lint, the admin screen, audit, request log, per-key limits, expiry notices | `admin/api-keys.md` (new, en+nb), `reference/identity.md`, `admin/overview` lists it |
| **B: the reference at /api** | Scalar bundled, spec served by session or key and filtered, `bearerAuth`, permission per operation, the compatibility policy, `oasdiff` in CI | `admin/api-keys.md` (the /api section, en+nb), `reference/api-compatibility.md` (new), `contributing/documentation.md` |
| **C: outgoing webhooks** | Subscriptions, signing, retries, delivery log | `admin/webhooks.md` (new, en+nb), `reference/` events catalog |
| **D: MCP server** | `/api/mcp` over the operations dispatcher, key-authenticated | `admin/mcp.md` (new, en+nb) |
| Later | OAuth apps, personal tokens, SDKs, idempotency keys | — |

A customer could be sold on all of this. The **landing site** (`vantigo-landing`) gets
an "API and integrations" mention once A and B land, written from the admin guide pages.

## 6. Risks

- **A leaked key is a breach.** The mitigations are:
  - prefix and checksum (scanners find it);
  - required expiry;
  - narrowing and IP allowlists;
  - the request log (you can see what it did);
  - instant revoke.
- **The API surface was internal.** Some operations may assume a browser, for example
  returning HTML, redirecting, or relying on cookies set mid-flow. Phase A should sweep
  the contracts for these and mark them `x-vantigo-api: never` rather than discover
  them through support tickets.
- **Personal data.** A key with `customers:personal-data` or Staff permissions can bulk
  export personal data. The request log is the processing record. The admin screen
  should say so when such a permission is granted.
- **Hot path.** Every key request is one hashed lookup. Cache resolved keys for a few
  seconds, and make sure revocation invalidates the cache.

## 7. Questions for the owner

Each question has a recommended default.

1. **Who may create keys?** Recommended: **Owners only, with MFA** (the OwnerManagement
   policy). The alternative is SystemAdmin too.
2. **How is scope chosen?** Recommended: **roles on the integration, optionally
   narrowed per key**. The alternative is a raw permission checklist per key, as in
   Tripletex.
3. **Expiry.** Recommended: **required, default 90 days, maximum 1 year**. The
   alternative also allows non-expiring keys, as in Odoo.
4. **Where the reference lives.** Recommended: **`/api` (HTML), with the spec behind a
   session or key**. The alternative is `/api/docs`, as the port design had it.
5. **Personal access tokens in phase 1?** Recommended: **no**. Integration keys first.
6. **After A and B, webhooks or MCP first?** Recommended: **webhooks**, because the
   outbox is ready and integrators ask for it first. MCP follows.
7. **Should the API be a priced add-on, as Fiken's is?** This is a product call, not an
   engineering one. Nothing in the design depends on it.
