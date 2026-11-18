# API access — integrations, scoped API keys and the reference at /api (phase 1)

Revision 3, 2026-11-17. Revision 2 followed an adversarial review of revision 1.
Revision 3 followed the plan's review and amends the spec in **§14, which wins where it
differs**. §15 lists what changed in each revision. This spec builds on the research
`docs/superpowers/research/2026-11-17-api-keys-and-api-reference.md` ("R"). It takes the
defaults R §7 recommends for the owner's questions, and §13 says what overturning each
one would change. Decisions are numbered K*n*. Code references are against `a9f140c82`,
whose latest migration is `00192`. Abbreviations:

- `srv/` is `apps/server/internal/`;
- `mig/` is `srv/db/migrations/`;
- `host/` is `apps/host/frontend/src/`.

## 1. Purpose

Administrators let external applications use Vantigo's API without a person's session.

- An **integration** is a named account that holds roles.
- An integration has one or more **API keys** that authenticate as it.
- An interactive reference at **`/api`** shows an integrator what their key can call,
  and lets them try it.

This is a feature of the identity module, not a new module. The feature is named **API
access** (Norwegian **API-tilgang**), and its screen lives in *Workspace admin*.

| Delivery | What it brings | Depends on |
| --- | --- | --- |
| **0** | The seams: user kind `integration` and every guard, listing and kind test widened to it; `x-vantigo-api` and its lint; the sweep; the never-held permissions | — |
| **A** | Integrations, keys, the `Check` branch, the admin screen, the audit trail, identity's first worker (expiry) | 0 |
| **B** | The request log, the per-integration rate limit, expiry notices | A |
| **C** | The reference at `/api`, the integrator's document, the compatibility policy, the breaking-change check | A |

B and C do not depend on each other.

**Not in this phase** (R §4; §12 says what each waits for):

- outgoing webhooks;
- an MCP server;
- OAuth apps;
- personal access tokens;
- CORS;
- idempotency keys;
- SDKs;
- a usage dashboard.

## 2. Decisions — delivery 0, the seams

### 2.1 The kind

- **K1 — User kind `integration`.** `identity.users.kind` gains `'integration'`, and
  `contracts.UserKindIntegration = "integration"`.
  - **No credentials.** An integration user has no password, no TOTP and no second
    factor. The `users_agent_no_credentials` CHECK already reads
    `kind = 'person' OR (…)`, so it holds as it is. It is renamed
    `users_nonperson_no_credentials`.
  - **An address that never routes:** `integration-<uuid>@integrations.invalid`
    (RFC 2606), as agents have.
  - **One way to make one.** An integration is created only through identity's
    integrations service (K6). No SCIM, invitation, OIDC link or user-admin endpoint
    can create one. `00141`'s kind-change trigger already stops a person from being
    turned into one.
  - **No built-in role.** An integration does not get the built-in User role. It holds
    only the roles it is given (K6).
- **K2 — The database guards apply to every non-person.** A new migration replaces
  `identity.refuse_agent_user` and `identity.refuse_agent_privileged_role` (`00141`)
  with versions that test `kind <> 'person'`, and keeps the trigger names. An
  integration can therefore never have:
  - a session, a login ticket, a passkey, an invitation, an OIDC link or a SCIM
    mapping;
  - a delegation, or the stewardship of a role;
  - the Owner or SystemAdmin role.

  `00142`'s agent-grant trigger keeps `kind = 'agent'`, because an integration is not
  an agent.
- **K3 — Integration roles have one writer.** An integration's `identity.user_roles`
  rows, and any access-group membership override naming it, may be written only by
  identity's integrations service (K19). Otherwise a delegate with a stewarded role
  could widen a key they hold through *Roles & access*, which launders the
  self-assignment ban (`srv/identity/authorization.go:832`). This is enforced twice:
  - **In the code.** `PutIdentityAccessUsersByIdRoles`
    (`srv/identity/authorization.go:823-905`) and the group-membership override refuse
    a target that is not a person, with `409 not_a_person`. Agents keep their existing
    path.
  - **In the database.** A trigger on `identity.user_roles` and on the membership
    override table refuses a row for an integration unless the transaction set
    `vantigo.integration_roles = 'on'` (`set_config(…, true)`). Only the integrations
    service sets it.

### 2.2 Every place that reads the kind

- **K4 — The kind sweep.** At `a9f140c82`, the kind is read in SQL, in Go and in the
  contracts. Every site is settled, and the plan names each one with its outcome.
  - **Listings and directories leave integrations out.**
    - `ListAccessUsers` (`queries/access.sql:146-150`) and the Users page
      (`queries/owner_users.sql:16-43`, people and agents, with their counts) gain
      `kind <> 'integration'`. Integrations are listed only on their own screen
      (K20).
    - `include_agents` in `queries/directory.sql:13, 39` comes to mean
      `kind IN ('person','agent')`, so modules that ask for agents get no
      integrations:
      - `agents/colleagues.go:112`, `agents/permissions.go:90`;
      - `knowledge/users.go:45`, `communications/conversations.go:359`.
    - Filters on `kind = 'person'` (mail addressees, passkeys, sessions, stations)
      already leave integrations out.
  - **Owner-tier user operations do not reach an integration.**
    - `managedTarget` (`srv/identity/users.go:86-102`) refuses any target that is not
      a person. That covers disable, delete, Staff manage/unmanage
      (`staff_managed.go:142`) and the SSO link. An integration is disabled,
      archived and audited only through K19.
    - `delegations.go:209` and `recovery.go:105` change the same way: `!isPerson` in
      place of `isAgent`.
  - **Go tests of the kind.** Every `== contracts.UserKindAgent` that means "not a
    person" becomes `!= contracts.UserKindPerson`. Each one that truly means "an
    agent" keeps its test, and the plan says which is which. The sites:
    - `agents/roles.go:185, 194`, `agents/subject.go:236`, `agents/views.go:67`;
    - `communications/conversations_notes.go:155, 174, 192`.
  - **`UserMayAct` is a person's or an agent's.** `UserMayAct` (`queries/sessions.sql:107`)
    and therefore `UserHolds`/`AsUser` stay for people and agents. They answer false
    for an integration. An integration acts only with a key on the request, never
    through `AsUser`. `UserHolds` documents this.
  - **Contracts.** The `UserEntry.Kind` docs (`contracts/users.go:22, 172`) name three
    kinds. `AccessUserResponse.kind` (`openapi/identity.yaml:127-133`) and
    `AssignableUserKind` (`openapi/communications.yaml:67-71`) stay `person|agent`.
    The listings above never return an integration.
  - **The directory pushes.** Entra's and Google's outbound syncs leave integrations
    out. The plan confirms which queries they enumerate users with.
  - **Display.** `contracts.UserEntry` lookups (by id, for "created by" columns) do
    return integrations. The host's `user-avatar.tsx` (which knows only `agent` at
    `:37`) gains an **API** badge.
  - **The Knowledge AI check.** `knowledge/server.go:42-51` (`viaAI`) treats any `Via`
    as AI. It is narrowed to `Via.Kind` `assistant` or `agent`. A key is not AI, and
    reads Knowledge as its roles allow.

### 2.3 The contract extension

- **K5 — `x-vantigo-api`.** A new operation extension with three values:
  - `allowed`: an API key may call the operation, where its access rule allows it
    (K7). Absent means `allowed`, except where K5's lint requires the mark.
  - `read`: like `allowed`, but a **read-only key** (K11) may also call it. It is
    meant for `POST` searches. Every `GET` and `HEAD` counts as `read` without the
    mark.
  - `never`: no key may call the operation.

  How it is enforced:
  - The router's `wrap` reads it into `contracts.Rule.API`, with the values
    `APIRefused` (zero), `APIAllowed` and `APIRead`. It does this as it reads
    `x-vantigo-station` and `x-vantigo-agent` (`srv/module/router.go:245-268`).
  - The zero value is refused, as `Rule.Station`'s is. A platform endpoint or the docs
    handler never admits a key unless it says so.
  - **`HasPermission(s)` does not inherit the zero value.** For a key principal,
    `contracts.HasPermissions` (`srv/contracts/access.go:185-203`) re-runs `Check`
    with `API: APIAllowed`. The outer `wrap` has already judged the operation's mark,
    and the handler's question is only about permissions. Without this, the 75
    handler-side checks in 20 modules would fail closed for keys, and they would do it
    silently: fields would be hidden and conditional writes refused.
  - **The lint** (`srv/openapi/lint.go`) refuses any other value. It also **requires
    an explicit mark** on:
    - every `anonymous` operation in every contract;
    - every operation in `identity.yaml`, `google.yaml` and `entra.yaml`.

    Those are where browser flows live. The lint test pins the list of `never`
    operations, so a change to it is visible in review.
- **K6 — The sweep.** Delivery 0 marks `x-vantigo-api: never` on:
  - **Every anonymous operation in identity:** login, two-factor login, passkey login,
    OIDC challenge, callback and complete, invitations, password recovery and
    bootstrap. Also every **station** endpoint. `principalUser` there
    (`srv/identity/stations.go:1081`) would treat an integration as a signed-in
    person.
  - **Inbound provider webhooks:** `meta/webhook`, Vipps, Storecove, Entra, Google.
    They have their own credentials.
  - **The calendar feed,** which has its own URL token.
  - **Delegated connect flows:** Outlook, Google and Entra connect, starts and
    callbacks. They redirect a browser. Google's and Entra's `sessionMAC(uuid.Nil)`
    (`google/personal.go:323`, `entra/outlook_connect.go:79`) would succeed silently
    for a key, so the mark is the only guard.
  - **Every handler that reads `SessionID`:** Google's personal connect
    (`google/personal_connect.go:76`), and everything that calls identity's
    `callerFrom` (`srv/identity/server.go:104-110`).
  - **Staff's account operations:** `putStaffPeopleByIdAccount` and
    `postStaffPeopleByIdAccountPass` (`openapi/staff.yaml:10204-10391`). They create a
    person's account and reveal its initial password.
  - **The Assistant's and the agents' conversation and run endpoints.**

  A file download (`Content-Disposition: attachment`) stays `allowed`.
- **K7 — Permissions a key never holds.** A key never holds `contracts.APINever`
  permissions, even when its integration's roles grant them. `permitted` refuses them
  for a key principal. The list is:
  - every `identity:*` and `agents:*` key;
  - every permission whose catalog entry is **not Delegable**. That is the Owner tier:
    `entra:manage`, `google:manage`, `knowledge:manage`, `accounting:close`,
    `orders:settings`, `purchasing:settings` and so on. The same family rule backs
    agents' `agentNever` (`srv/agents/template.go:286-316`).

  `APINever` is computed from the composed catalog, so a new non-Delegable permission
  joins it without an edit. A contract test asserts that no `x-vantigo-api: allowed`
  operation names only `APINever` keys. Such an operation should be marked `never`
  instead. The screen shows these permissions as "never available to API keys" in the
  role summary (K20).

## 3. Decisions — delivery A, integrations and keys

### 3.1 The model

- **K8 — An integration.** Table `identity.integrations`:

  | Column | Meaning |
  | --- | --- |
  | `user_id` | PK, FK `identity.users` ON DELETE RESTRICT, kind `integration` |
  | `name` | Unique among non-archived integrations, 1–100 characters |
  | `description` | Optional, up to 1,000 characters |
  | `contact_user_id` | Optional; the person responsible, who gets the notices (K25); FK ON DELETE SET NULL |
  | `rate_limit_per_minute` | 60–6,000, default 600 (K24) |
  | `created_by`, `created_at` | `created_by` FK ON DELETE SET NULL |
  | `archived_at`, `archived_by` | `archived_by` FK ON DELETE SET NULL |
  | `version` | Optimistic concurrency, as identity's other rows have |

  - The user row carries the display name (= `name`) and `is_disabled`.
  - **Disabling** refuses every key at once, and can be undone.
  - **Archiving** revokes every key, keeps the row for attribution, frees the name, and
    cannot be undone.
  - The integration's roles are ordinary `identity.user_roles` rows on its user,
    written only as K3 allows. Custom roles, access groups and
    `EffectivePermissionForUser` work unchanged.
- **K9 — What a key reaches.** `Check` meets a valid key by the rule's kind:

  | Rule | A valid key |
  | --- | --- |
  | `permission:…` | Evaluated as the integration's user (`evaluate`). Every key named must be held through its roles, not be in `APINever` (K7), and, when the key is narrowed, be in its narrowing |
  | `anonymous` | The principal is the integration, so a handler may attribute to it |
  | `session` | `403 forbidden`. An account's self-service is a person's. This includes the operations marked `x-vantigo-agent: allowed`. Keys get no such opt-in in this phase (§12) |
  | `policy:…` | `403 forbidden`. A key never manages users, roles, SSO, the workspace or keys |
  | `scim` | Not a key request. The SCIM bearer is read as today, and a `vtg_` token fails it with `401` |

  Before the rule is evaluated, two marks are checked:
  - An operation whose `Rule.API` is refused answers `403 api_not_available`.
  - A **read-only key** (K11) on an operation that is not `read` answers
    `403 api_key_read_only`.

  `businessSatisfied` (`access.go:542`) holds only Owners to MFA, and an integration is
  never an Owner (K2), so it does not apply.
- **K10 — The principal.** `contracts.Principal` for a key has:
  - `UserID`: the integration's user;
  - `Roles`: its roles;
  - no `SessionID`;
  - `MFAVerified` false;
  - `Via{Kind: "api_key", KeyID}`.

  `contracts.ViaAPIKey = "api_key"` and `Via.KeyID uuid.UUID` are added. The
  integration is the `UserID`, so no second field is needed. Modules attribute to
  `UserID` as they do today, which makes `created_by` the integration.

### 3.2 The key

- **K11 — The credential.**
  - **Format:** `vtg_` + the 32 random bytes in base62, left-padded to 43 characters +
    the CRC32 of the random part in base62, left-padded to 6 characters. That is 53
    characters.
    - The checksum lets secret scanners, and `Check`, reject a mistyped or look-alike
      string without a database read. It is not a security control.
    - Identity's `hashToken` (`srv/identity/tokens.go`) is reused for storage.
      `newToken` and `parseToken` are not, because they are base64url.
  - **Table `identity.api_keys`:**
    - `id`, `integration_id` (FK ON DELETE RESTRICT);
    - `name`, 1–100 characters, e.g. "Production";
    - `token_hash`, SHA-256, unique;
    - `display`: `vtg_` + the first 4 random characters + `…` + the key's last 4,
      which belong to the checksum (K57);
    - `permissions text[]`: the **narrowing**. NULL means none. Otherwise the key may
      use only the permissions listed, of those its integration's roles grant;
    - `read_only boolean`: the key calls only `read` operations (K5);
    - `allowed_networks cidr[]`: empty means any (K14);
    - `expires_at`: required (K12);
    - `created_by`, `created_at`;
    - `revoked_at`, `revoked_by`;
    - `rolled_from`: FK to `api_keys` ON DELETE SET NULL, with a **unique partial
      index** where not null (K13);
    - `last_used_at`, `last_used_ip`.

    Every FK to a person is ON DELETE SET NULL, so deleting an Owner who made a key is
    never refused.
  - **Narrowing in the screen.** A key name in the narrowing that the roles do not
    grant is allowed. It does nothing until the roles grant it, and the screen marks it
    "not granted by the roles". A name a later release removes from the catalog is kept
    and ignored, and the screen strikes it through.
  - **Shown once.** The secret appears only in the response that creates or rolls a
    key. That response carries `Cache-Control: no-store`. The secret is never logged,
    never in an error, never in the audit trail and never in the request log.
    - Tracing records no request headers today. A test asserts that no span attribute
      or log line contains `vtg_`.
- **K12 — Expiry.** Every key expires.
  - The default is 90 days. The choices are 30, 90, 180 or 365 days, or a date up to
    365 days ahead.
  - An expired key is refused with `401 api_key_expired`.
  - Identity's **expiry worker** (K15) records `api_key.expired` once, and deletes a
    key's row 90 days after it expires. The audit rows stay.
- **K13 — Roll and revoke.**
  - **Roll** issues a new key with the same name, narrowing, read-only flag, networks
    and a new expiry.
    - The old key's `expires_at` becomes the earlier of its own expiry and
      `now + overlap`. The overlap is 0 hours to 7 days, default 24 hours.
    - `rolled_from` links the two keys. A second roll of the same key fails the unique
      index and answers `409 api_key_already_rolled`.
  - **Revoke** sets `revoked_at`. The next request with the key is refused with
    `401 unauthenticated`.
  - Nothing caches a resolved key (K17), so a revocation takes effect at once on every
    replica.
- **K14 — Networks.** `allowed_networks` lists CIDRs, IPv4 or IPv6, at most 20.
  - A request from outside them answers **`401 unauthenticated`**, as a station's
    out-of-network request does. That does not confirm to the caller that the key is
    valid.
  - The real reason, `network`, is recorded in the request log (K23), and
    `last_used_*` is not updated.
  - The address is `httpx.ClientIP`, which honours the configured trusted proxies
    (`srv/httpx/forwarded.go:85`).
- **K15 — Identity's first worker.** Identity registers no `Workers` today; its purges
  run inline. Delivery A adds `identity.api-keys`. It is registered in
  `module.Module.Workers`, runs in worker mode, and holds a lease, copying an existing
  module's leased worker (the plan names the one). It runs hourly and:
  - records `api_key.expired` for keys that have passed their expiry since the last
    run;
  - deletes key rows 90 days past expiry.

  Delivery B extends it (K23, K25). `admin/configuration.md` names it with the other
  workers.

### 3.3 The `Check` branch

- **K16 — Where the key is read.** `Access.Check` (`srv/identity/access.go:108-153`)
  gains a branch after the grant and SCIM branches and **before** the station branch.
  - **The trigger** is a request whose single `Authorization` header is `Bearer vtg_…`.
    The scheme is case-insensitive.
  - **Precedence over a cookie.** Such a request is judged as a key request only, and
    no session cookie it also carries is read. This is the precedence stations have
    (M134). A browser on `/api` that sends a key therefore acts as the key.
  - **Several `Authorization` headers** answer `401 unauthenticated` on every rule but
    SCIM's. `scimBearer` joins them (`scim.go:160`), while `isStationRequest` reads the
    first (`access_station.go:101`), and a key must not be judged ambiguously.
  - **A `Bearer` token without the prefix** is not a key, and the request falls through
    as today.
  - **A malformed key** answers `401 unauthenticated`: wrong length, or a bad checksum.
  - **Under an `anonymous` rule,** an invalid key is still `401`. A dead session there is
    an anonymous caller instead, but a client that presented a credential should learn
    that it failed.
- **K17 — Resolving.**
  1. One indexed read: `api_keys` by `token_hash`, joined to the integration and its
     user. The read asserts `kind = 'integration'`, `is_disabled`, `archived_at`,
     `revoked_at` and `expires_at`.
  2. A key that is not found, revoked, disabled or archived answers
     `401 unauthenticated`. An expired key answers `401 api_key_expired`.
  3. The roles come through `evaluate`, with a `subject` of the integration's user,
     built as `checkAgentGrant` builds one (`agent_grants.go:84-88`).

  There is no cache. The cost is the same as a session's lookup, which also writes its
  idle window. `last_used_at` and `last_used_ip` are updated with
  `UPDATE … WHERE last_used_at IS NULL OR last_used_at < now() - interval '1 minute'`.
  That is at most one write a minute per key, outside the request's transaction.
- **K18 — Refusals keep the session.** `Access.Reject` (`access.go:255-257`) clears
  the session cookie on a 401 unless the request is a station's. It now also leaves the
  cookie alone for a key request, so a bad key typed into `/api` never signs the admin
  out.
- **K19 — Error codes.** These are written by `authError`, in its existing
  `AuthErrorResponse` body (`srv/identity/errors.go:95`).

  | Status | Code | When |
  | --- | --- | --- |
  | 401 | `unauthenticated` | Malformed, unknown, revoked, disabled or archived key; outside its networks; several `Authorization` headers |
  | 401 | `api_key_expired` | The key matched but has expired |
  | 403 | `forbidden` | Session or policy rule, or a permission the key does not reach |
  | 403 | `api_not_available` | The operation is `x-vantigo-api: never` |
  | 403 | `api_key_read_only` | A read-only key on an operation that is not `read` |
  | 409 | `api_key_already_rolled` | K13 |
  | 429 | `rate_limited` | K24; written by `ratelimit.Reject` in its own body |

  A key's 401 carries `WWW-Authenticate: Bearer realm="vantigo"`.
- **K20 — The rest of the request path.**
  - **CSRF.** A server-side client sends neither `Sec-Fetch-Site` nor `Origin`, so it
    passes (`srv/server/server.go:50-62`). The browser on `/api` is same-origin, so it
    passes too.
  - **CORS.** None is added (§12).
  - **The live socket** (`/api/v1/live`) stays session-only or station-only, and checks
    `Origin` itself (`srv/live/handler.go:25-75`).
  - **The operations dispatcher's grants** never come from a header, and its paths are
    `never` (K6).

### 3.4 Who manages integrations, and where

- **K21 — The endpoints.** Every endpoint below is `policy:Owner+OwnerManagement`
  (Owners, with MFA when `OwnersRequireMFA` is set), `x-vantigo-api: never` and
  `x-vantigo-assistant: never`. The Assistant never creates a credential. The
  endpoints are under `/api/v1/identity/integrations`, and these are the only writers
  K3 allows.

  | Method and path | Does |
  | --- | --- |
  | `GET /` | List, with each integration's key count, its nearest expiry and its last use |
  | `POST /` | Create: name, description, contact, roles |
  | `GET /{id}` | Read, with its keys (never the secrets) and its effective permissions |
  | `PATCH /{id}` | Name, description, contact, rate limit; `If-Match` |
  | `PUT /{id}/roles` | Replace its roles |
  | `POST /{id}/disable`, `POST /{id}/enable` | |
  | `POST /{id}/archive` | Revoke every key, then archive |
  | `POST /{id}/keys` | Create a key: name, expiry, narrowing, read-only, networks. Answers the secret once |
  | `POST /{id}/keys/{keyId}/roll` | Overlap. Answers the new secret once |
  | `POST /{id}/keys/{keyId}/revoke` | |
  | `GET /{id}/requests` | The request log (K23), delivery B |

- **K22 — The screen.** *Workspace admin › API access*, at
  `host/routes/workspace/api-access.tsx`. Its Norwegian name is *API-tilgang*. It is a
  navigation item in `host/apps.ts` beside *Stations*, marked `ownerOnly: true`, and it
  is in the spotlight.
  - **The list:** name, status (active, disabled or archived), roles, the number of
    keys, the nearest expiry and the last use.
  - **An integration's page:**
    - Its details.
    - Its roles. Custom roles are chosen with the picker *Roles & access* uses
      (`routes/workspace/roles.tsx`). The Users page's picker offers only Owner and
      User. The page then shows the effective permissions, with K7's permissions
      marked "never available to API keys".
    - Its keys: name, display, expiry, a narrowing summary, read-only, networks, the
      last use and its IP, and *Roll* and *Revoke*.
    - The request log (delivery B).
  - **The key creation dialog:**
    - The narrowing is a checklist grouped by module, as the role editor is.
    - *Read only* is a switch, not a preset: what it allows is decided by the
      operation (K5), not by the permission's name.
    - The dialog shows the secret once, with *Copy* and a warning that it cannot be
      shown again, and a `curl` example against the installation's own URL.
  - **Sensitive permissions** (`contracts.Permission.Sensitive`), reached through the
    roles or named in a narrowing, are badged. The screen says that a key holding them
    can read or change that data in bulk, and that the request log is the record of it.

### 3.5 The audit trail

- **K22a — What is audited.** Identity's audit trail (`srv/identity/audit.go`) records
  these actions on the change's own transaction, with the acting Owner and their MFA:
  - `integration.created`, `integration.updated`, `integration.roles_changed`;
  - `integration.disabled`, `integration.enabled`, `integration.archived`;
  - `api_key.created`, `api_key.rolled`, `api_key.revoked`;
  - `api_key.expired`, written by K15's worker with `system:api-keys` as the actor
    (`audit.go:22`).

  `before` and `after` carry names, roles, the narrowing, read-only, networks and the
  expiry. They never carry a hash or a secret. `targetUser` is the integration's user.

## 4. Decisions — delivery B, the request log, the limit and the notices

- **K23 — The request log.** Table `identity.api_requests`. It holds `at`, `key_id`,
  `integration_id`, `method`, `operation_id`, `status`, `reason` (for refusals:
  `expired`, `revoked`, `network`, `read_only`, `never`, `forbidden`, `rate_limited`),
  `duration_ms`, `client_ip` and `trace_id`. It never holds a body, a query string or a
  path parameter value, because those can carry personal data. The operation id and the
  trace id are enough to find a request in the server's own traces.
  - **Who writes it.** The module package cannot write identity's tables. A recorder
    port is added instead, `contracts.APIRequestRecorder`, which identity provides.
    It is set on `module.Options` as `Access` is.
    - `wrap` calls it after the handler answers.
    - For a refusal, `Check` returns a typed error, `contracts.APIKeyRefusal{KeyID,
      IntegrationID, Reason}`, wrapping the status error. `wrap` can then attribute a
      refused request whose key matched.
    - A key that matched nothing is not logged, because there is nothing to attribute
      it to.
  - **Buffered writes.** A buffered writer flushes every 2 seconds or every 500 rows,
    on its own connection, and on shutdown. A crash may lose the last two seconds. The
    log is an operational record, not the audit trail.
  - **Partitions.** The table is partitioned by day on `at`, following the precedent
    of `energy.consumption_intervals` (`mig/00005_energy_baseline.sql:122-160`): an
    `identity.ensure_api_requests_partition(day)` function, partitions created ahead,
    and a **DEFAULT partition** so that a write never fails when the worker lags.
    - K15's worker creates the next 7 days' partitions on each run.
    - It drops partitions older than **30 days**.
    - It moves anything that landed in DEFAULT into its day.
  - **The screen** shows the last 30 days:
    - counts per day and per status class;
    - the operations called most;
    - the most recent 200 requests.

    It filters by key, by status class and by reason.
- **K24 — The rate limit, per integration.**
  - A key request is counted against `ratelimit.Policy{Name: "api", Limit:
    integration.rate_limit_per_minute, Window: 1m}`, keyed by the **integration**.
    Adding keys does not multiply the limit.
  - It is counted **after** `Check` resolves the key. Today's limits run before
    `Check`, keyed by IP (`router.go:315-325`).
  - The per-operation, IP-keyed limits that modules list still apply.
  - `ratelimit.Decision` gains `Remaining int` and `Reset time.Time`. Every key
    response then carries `RateLimit-Limit`, `RateLimit-Remaining` and
    `RateLimit-Reset`.
  - A refusal answers `429 rate_limited` with `Retry-After`, in `ratelimit.Reject`'s
    body.
  - The counter is one upsert on one `platform.rate_limit` row per integration and
    minute, so concurrent requests from one integration contend on that row. The plan
    measures it at 600 requests a minute. If it costs too much, the plan adds an
    in-process pre-count that batches into the shared row every second, and keeps the
    shared row so that replicas agree.
- **K25 — Expiry notices.**
  - K15's worker sends a notice 14 days before a key expires, 3 days before, and on
    the day.
  - It goes to the integration's contact, if that person is active, and to every
    active Owner. A disabled contact is kept on the integration, but skipped.
  - It is system mail, in the recipient's language, through the mailer identity
    already uses for invitations.
  - A key that is rolled or revoked first gets none.
  - What has been sent is recorded in `identity.api_key_notices`, keyed by
    `(key_id, kind)`, so no notice is sent twice.

## 5. Decisions — delivery C, the reference at /api

- **K26 — The page.**
  - `GET /api` and `HEAD /api` serve the reference page when `Accept` prefers
    `text/html`.
  - Any other request to `/api` keeps the 404 response (`srv/module/compose.go:682-683`),
    so an API client still gets the API's own error for an unknown path.
  - The page is **public**, as `/docs` is. It is a static shell that names nothing
    about the installation.
  - It is served on `APP_URL`'s host only. The public host's allowlist
    (`srv/security/publichost.go`) does not admit it.
- **K27 — Scalar, bundled.**
  - **Packaging.**
    - `@scalar/api-reference`'s standalone bundle is pinned in a `package.json` of a
      new workspace package, `apps/apiref`.
    - The bundle is built and copied into `srv/apiref` before `go build`, by the same
      overlay mechanism the SPA and the docs use (`scripts/spa-embed-overlay.sh`).
    - It is embedded with `//go:embed` and served at `GET /api/reference/{file}` with
      immutable caching.
    - Dependabot bumps it with the other frontend dependencies.
  - **Nothing leaves the installation.**
    - There is no CDN and no external font (`withDefaultFonts: false`).
    - There is no Scalar proxy (`proxyUrl` unset), so requests go same-origin.
    - Telemetry and "open in Scalar" are off.
    - The MIT licence joins the third-party notices.
  - **The page's own script.** A small script in `apps/apiref`, served from `'self'`,
    holds a key field above Scalar.
    - On submit, the script fetches the document with the key (K28), then mounts
      Scalar with the document as `content` and the key as the bearer token.
    - It sets `persistAuth: false`. The key lives in memory only, never in the app
      origin's storage.
    - Clearing the field remounts Scalar without the key.
  - **CSP.** The page has its own policy, as `/docs` does
    (`srv/docs/handler.go:25-39`):
    - `default-src 'none'`;
    - `script-src 'self'`;
    - `style-src 'self' 'unsafe-inline'` (Scalar injects styles);
    - `connect-src 'self'`;
    - `img-src 'self' data:`;
    - `font-src 'self'`;
    - `frame-ancestors 'none'`.

    The plan's first step checks whether Scalar's bundle runs without `unsafe-eval`.
    If it does not, the page alone adds what it needs, and the plan records it.
  - **Smoke test.** A test loads the page and finds the operation list.
- **K28 — The document it loads.** `GET /api/openapi.json` stops using `sessionRule`.
  - **Its own check.** It calls `Check` with
    `Rule{Kind: RuleAnonymous, API: APIAllowed}`. A principal with no user answers
    `401`. A session or a key passes. An invalid key is already `401` (K16).
  - **Under a session, without a parameter:** the full merged contract, unchanged.
    Contributors and the installation guide use it.
  - **Under a session, with `?view=api`: the integrator's document.** It holds every
    operation an API key could call: rule `permission` or `anonymous`, `Rule.API` not
    refused, and not naming only `APINever` keys. The page loads this view for a
    signed-in admin.
  - **Under a key:** the integrator's document, filtered to what the key reaches. That
    is its integration's **effective** permissions (roles and access groups, a new
    `EffectivePermissionsForUser` query; `access.sql:~135` reads direct roles only)
    minus `APINever`, intersected with the narrowing. A read-only key sees `read`
    operations only. Anonymous operations that are `allowed` are always included.
  - **What the integrator's document adds:**
    - `components.securitySchemes.bearerAuth` (HTTP bearer, `vtg_…`), and a top-level
      `security` naming it. Anonymous operations get `security: []`.
    - `servers: [{url: APP_URL}]`.
    - On every operation, the responses `401`, `403` and `429` with K19's codes, so
      that integrators and the contract conformance tests never see an undeclared
      status.
    - In each operation's description, the permission it needs: *Requires
      `customers:view`*.
    - An `info.description` that links to the compatibility policy (K30) and to the
      admin page that makes keys.
    - The `x-vantigo-*` extensions are stripped.
  - **Caching.** Each document is built once per composition and per filter. A filtered
    document is cached by the sorted effective permission set and the read-only flag.
- **K29 — Without a key.**
  - With no key and no session, the page explains how to get a key and links to
    docs.vantigo.io's static reference.
  - A signed-in person who has not entered a key sees the `?view=api` document.
  - A call they try without a key goes with their session cookie and is judged as
    their own call. The page says so above the client.
- **K30 — The compatibility policy.** A new reference page,
  `reference/api-compatibility.md`, states:
  - Within `/api/v1`, changes are **additive**: new operations, new optional request
    fields, new response fields, and new enum values in responses. Clients must ignore
    unknown response fields and unknown enum values.
  - A breaking change to an operation a key can call ships as a new operation or a new
    version, never in place.
  - A deprecation is announced in the release notes and with `Deprecation` and
    `Sunset` headers on the operation, at least 6 months before removal.
  - Operations that are `x-vantigo-api: never` are outside the promise.
- **K31 — The breaking-change check.**
  - `srv/openapi/cmd/contract` gains an `integrator` subcommand. It writes the
    integrator's document from `openapi/*.yaml`, with every module enabled.
  - CI's `lint_server` job builds that document for the pull request and for its base,
    and runs `oasdiff breaking` on the pair.
  - A breaking finding fails the job unless the commit marks it with Conventional
    Commits' `!` (for example `feat(orders)!: …`) and an
    `API-Breaking: <operationId> — <reason>` trailer.
  - The plan confirms that goreleaser's changelog (`.goreleaser.yaml:99`) shows `!`
    commits under their own heading, and adds a group for them if it does not.
- **K32 — docs.vantigo.io.**
  - The static reference keeps rendering every module's contract.
  - The Agents and Meta contracts, missing from `docs/astro.config.mjs:14-36`, are
    added.
  - It gains an introduction page under `reference/api/`, written by hand, which the
    generator leaves alone. The page covers:
    - authentication with a key;
    - K19's codes;
    - K24's headers;
    - the compatibility policy.

## 6. Events

None in this phase. The webhooks phase (§12) subscribes to the existing outbox events.
It needs nothing from this phase beyond the integration as the subscriber.

## 7. Permissions

- **No new permission key.** Managing integrations is the Owner tier (K21). An Owner
  can give an integration any role except Owner and SystemAdmin, so the person making
  a key must be able to give any role.
- **`contracts.APINever`** (K7) is computed from the catalog and needs no new field.

## 8. Data model (schema `identity`)

| Table | Delivery | Notes |
| --- | --- | --- |
| `users.kind` | 0 | Gains `'integration'`; guards widened (K1, K2); role writes guarded (K3) |
| `integrations` | A | K8 |
| `api_keys` | A | K11; unique `token_hash`; index `(integration_id)`; unique partial index `(rolled_from)`; partial index on `expires_at` where not revoked |
| `api_key_notices` | B | `(key_id, kind)` primary key (K25) |
| `api_requests` | B | K23; partitioned by day on `at`, with DEFAULT; index `(key_id, at desc)` |

## 9. Platform, registries and documentation

- **Contracts.**
  - `openapi/identity.yaml` gains K21's endpoints.
  - Every contract gains K5's and K6's marks.
  - The integrator's document adds K19's responses at build time (K28), so the module
    contracts do not repeat them.
- **No module registry changes.** This is not a new module.
- **The Assistant.** Its link prefixes do not change, because admin screens are not
  linked from it by design (`srv/assistant/prompt.go:21-60`,
  `apps/assistant/frontend/src/lib/links.ts:16-38`).
- **The host.**
  - The route, `i18n.ts` in English and Norwegian, the `apps.ts` navigation item and
    the spotlight entry.
  - The avatar's API badge (K4).
  - The identity endpoints already go through `"@vantigo/app"` in `sessionClients`, so
    no new entry is needed.
- **Documentation.** It lands in the same commit series as the code (AGENTS.md).
  `bun run tools/docs/check-coverage.ts --list` is read at the start of each delivery,
  and every page whose `sources` the delivery touches is updated.

  | Page | Delivery | Language |
  | --- | --- | --- |
  | `admin/api-access.md`, new: create an integration, give it roles, make, roll and revoke a key, narrow it, make it read-only, restrict networks, read the request log, what to do when a key leaks | A, B | en + nb |
  | `admin/index.md`: lists the page | A | en + nb |
  | `admin/authentication.md`: integrations and keys as a way to authenticate; K9's table; K19's codes; the audit actions. Its `sources` cover `srv/identity`, `srv/module/router.go` and `srv/live/handler.go` | 0, A, B | en + nb |
  | `admin/configuration.md`: the `identity.api-keys` worker (K15) | A | en + nb |
  | `user/workspace-administration.md`: the *API access* screen, as a task | A | en + nb |
  | `admin/installation.md`, `admin/transport-security.md`: `/api`, and `/api/openapi.json` admitting a key | C | en + nb |
  | `reference/api-compatibility.md`, new (K30) | C | en |
  | The `reference/api/` introduction (K32) | C | en |
  | `contributing/documentation.md`: the Agents and Meta contracts in the reference; `x-vantigo-api` and the `API-Breaking` trailer | 0, C | en |
  | `contributing/module-boundaries.md`: `x-vantigo-api` with the other contract extensions; "a new non-Delegable permission is never available to keys" | 0 | en |

  After each docs edit, run `go generate` in `apps/server`, because the Assistant
  embeds the docs.
- **The public site.** A customer could be sold on this. Once A and C land, the landing
  repository (`vantigo-landing`) gains an "API and integrations" section on its front
  page, written from `user/workspace-administration.md` as AGENTS.md requires. It is a
  separate pull request, linked to this repository's.

## 10. Edge cases

- **An integration's roles change while a key is mid-request.** `evaluate` reads the
  roles once per `Check`, so the next request sees the new roles.
- **A role an integration holds is deleted.** Its `user_roles` rows go, as they do for
  people, and the key loses those permissions.
- **The contact person is disabled.** The contact is kept and skipped (K25). If the
  person is deleted, the FK sets it to NULL, and the screen shows "no contact".
- **A module is disabled.** Its operations are not mounted, and the integrator's
  document leaves them out. A narrowing that names its permissions keeps them for when
  the module returns.
- **A key is sent in a query string or as a cookie.** It is not read. Only the
  `Authorization` header is.
- **`OwnersRequireMFA` is turned on.** Keys are unaffected. Integrations are never
  Owners (K2), and the Owner tier's MFA is a rule about a session.
- **A delegate steward tries to give an integration a role.** Refused, `409
  not_a_person` (K3). The database refuses it too.
- **The Assistant or an agent tries to read the request log.** Refused: K21's
  endpoints are policy-guarded and `x-vantigo-assistant: never`.
- **Several replicas.** Nothing is cached per process except the filtered documents,
  which are keyed by permission set. Revocation, limits and `last_used_*` live in
  Postgres.

## 11. Testing

- **Identity, unit and integration:**
  - K9's matrix, against one operation of each rule kind, with and without a narrowing
    and read-only;
  - `APINever` (K7);
  - every refusal in K19;
  - precedence over a session cookie, and several `Authorization` headers (K16);
  - `Reject` leaving the cookie (K18);
  - K2's triggers for an integration;
  - K3's refusal, in code and in the database;
  - K4's listings, with users of all three kinds;
  - `managedTarget`.
- **Handler-side checks (C2's guard):** for one handler-gated field or conditional
  write per module that has one (for example accounting's `permClose` at
  `vouchers.go:439`, and knowledge's `canManage`), a key holding the permission gets
  the same answer a person holding it gets.
- **Contract-wide:** for every mounted operation not marked `never`, a key holding its
  permissions gets neither a 500 nor an unexpected 401. K5's lint pins the `never`
  list.
- **The request log:** the writer flushes on size, on time and on shutdown; refusals
  are attributed through `APIKeyRefusal`; partitions are created ahead; DEFAULT is
  drained; partitions past 30 days are dropped.
- **The rate limit:** the 601st request in a minute, across two keys of one
  integration, answers 429 with the headers.
- **`/api/openapi.json`:** under a session, a session with `?view=api`, a narrowed key
  and a read-only key. Each document validates with kin-openapi and declares 401, 403
  and 429.
- **Secrets:** no log line, span or request-log row contains `vtg_`.
- **The page:** the CSP header; `Accept` negotiation; the assets served from the
  binary; no request to another origin; the key never written to storage (Playwright
  in the host's end-to-end suite).
- **The frontend:** the creation dialog shows the secret once; the role summary marks
  `APINever`; the avatar badge.
- **Seed data:** the development seed gains one integration, "Webshop sync", with a
  custom role reaching Customers, Products and Orders. It has one key, printed by the
  seed and never written to the repository.

## 12. Later, and what each waits for

- **Outgoing webhooks.** Its own research and spec. Integrations subscribe to the
  outbox's events, with Standard Webhooks signing, retries and a delivery log. It waits
  for nothing beyond delivery A.
- **An MCP server at `/api/mcp`.** Its own spec. It would be the operations dispatcher
  with the `x-vantigo-assistant` flags, authenticated by a key. It needs a decision on
  whether a key may drive the Assistant's tools, which K6 marks `never` for now.
- **Session rules marked `x-vantigo-agent: allowed`.** Today these are the accounting
  supplier operations. An `x-vantigo-api` opt-in for them, with the handler checking
  the permissions, follows when an integrator needs one.
- **OAuth 2.0 apps** (authorization code + PKCE, client credentials). These are for
  outside vendors selling apps to many installations. An OAuth client would act as an
  integration.
- **Personal access tokens.** They raise offboarding and Owner MFA questions that
  integrations avoid (R §4.4).
- **CORS for browser apps.** Not planned. A browser app should use OAuth with PKCE.
- **`Idempotency-Key`** on creating operations. It waits for the webhooks phase, which
  has the same replay question.
- **Generated SDKs and a usage dashboard.** These follow the compatibility policy
  (K30).

## 13. Open questions

Each question takes R §7's default. An overturn changes the following:

1. **SystemAdmin manages integrations too.** K21's policy admits SystemAdmin, and the
   role picker then offers only the roles a SystemAdmin may assign.
2. **Scope is a raw permission checklist per key, with no roles.** K8 loses its roles,
   the narrowing becomes the whole grant, and `evaluate` needs a subject with an
   explicit permission set. It is more code, and an integration no longer follows a
   role when the role is edited.
3. **Keys may never expire.** K12 gains "never", K25 has nothing to send for those
   keys, and the screen marks them in red.
4. **The reference is at `/api/docs`.** K26's path changes, and `/api` keeps its 404
   for everyone.
5. **Personal access tokens in this phase.** A new kind of key on a person's user. The
   Owner MFA rule (`access.go:535-544`) and offboarding need their own decisions.
6. **MCP comes before webhooks.** Only §12's order changes.
7. **The API is a priced add-on.** A workspace setting gates delivery A's `Check`
   branch and screen. That is the owner's product call.
8. **Integrations count as users for licensing.** By default they are **not** counted,
   because K4 leaves them out of the Users page counts. If they should be, the owner
   counts (`owner_users.sql:42-43`) gain a third column.

## 14. Amendments (revision 3, from the plan’s review)

- **K33 (amends K5): the inner check is `APIRead`.** For a key principal,
  `HasPermissions` re-runs `Check` with `API: APIRead`, because the outer `wrap` has
  already judged the operation's mark and its read-only rule. With `APIAllowed`, a
  read-only key would fail every handler-side check, even on a `GET`.
- **K34 (amends K19, K23): the refusal errors wrap the sentinels.**
  - `ErrAPINotAvailable` and `ErrAPIKeyReadOnly` are `fmt.Errorf("%w: …",
    ErrForbidden)`, and `ErrAPIKeyExpired` wraps `ErrUnauthenticated`. This is how
    `ErrStationScope` is defined (`srv/contracts/access.go:218`).
  - `APIKeyRefusal` unwraps to them.
  - `wrap`, `compose.go` and the live handler send anything else to `httpx.WriteError`
    as a 500, so wrapping keeps them answering 401 or 403.
  - `Reject` branches on each to write its code.
- **K35 (amends K23): the recorder is found by type assertion.** `module.Options` does
  not exist, and `RouterOptions` is built in 23 modules. `contracts.APIRequestRecorder`
  is instead implemented by identity's `Access` beside `Access`, as `StationAccess` is
  (`srv/contracts/access.go:220-225`). `wrap` finds it with
  `r.opts.Access.(contracts.APIRequestRecorder)`. The buffered writer is started and
  closed where `identity.NewAccess` is built (`apps/server/cmd/vantigo/main.go:315`).
- **K36 (amends K28): the key's scope is a contract.** `srv/module/compose.go`, which
  serves `/api/openapi.json`, cannot import identity. `contracts.APIKeyScope`, with
  `Scope(ctx, Principal) (permissions []string, readOnly bool, err error)`, is
  implemented by identity's `Access` and found by type assertion. It answers the
  effective permissions minus `APINever`, intersected with the narrowing.
- **K37 (amends K31): the integrator's document is built from the composed catalog.**
  - `srv/openapi` may not import business modules (depguard), and `Delegable` lives in
    each module's Go code.
  - The builder, `srv/openapi.Integrator(contract, catalog, filter)`, therefore takes
    the catalog as a parameter.
  - It is called by `compose.go`, and by a new `vantigo openapi integrator` subcommand
    in `apps/server/cmd/vantigo`, which composes every module. CI runs that subcommand.
  - The CI check:
    - runs on pull requests only;
    - fetches the base commit explicitly;
    - installs `oasdiff` through the mise action's `install_args`;
    - is skipped when the base has no `integrator` subcommand.
- **K38 (amends K31): release notes.** Real releases use `gh release create
  --generate-notes`; `.goreleaser.yaml` is a dry run. A *Breaking API changes* category
  is therefore added in `.github/release.yml`, keyed on a `breaking-api` label that the
  CI check applies to the PR. goreleaser's groups are left alone.
- **K39 (amends K23): the DEFAULT partition is drained before a day is created.** For
  each day that is missing, one transaction:
  1. moves that day's rows out of DEFAULT into a temporary table;
  2. creates the partition;
  3. inserts the rows back.

  Postgres refuses to create a partition while DEFAULT holds rows for it.
  `api_requests` has no FK to `api_keys`, because a key's row is deleted 90 days after
  expiry and its log rows age out on their own. When the worker is switched off
  (`IDENTITY_API_KEYS_ENABLED=false`), no partitions are made and everything lands in
  DEFAULT. `admin/configuration.md` says so.
- **K40 (amends K27, §11): no new browser suite.** The repository has no Playwright.
  The page's checks are split three ways:
  - **Go tests:** the CSP header, the assets, `Accept` handling and the public host.
  - **vitest with jsdom:** the page script sends the key in the header and writes
    nothing to storage.
  - **The manual browser check:** "try it" and "no request leaves the origin".
- **K41 (amends K27): packaging.**
  - The page's package is `apps/apiref/frontend`, so it is a bun workspace, it is
    linted, and Dependabot covers it.
  - `scripts/spa-embed-overlay.sh` and `scripts/restore-embed-overlay.sh` gain
    `srv/apiref/dist`.
  - Scalar's MIT notice joins the third-party notices.
  - `srv/apiref` joins depguard's platform list.
- **K42 (amends K32): the introduction is `reference/api-introduction.md`.**
  `reference/api/` stays generated-only, as AGENTS.md and
  `contributing/documentation.md` say.
- **K43 (amends K4): the avatar badge is the shell's.** `user-avatar.tsx` is
  `packages/frontend-shell/src/ui/user-avatar.tsx`.
- **K44 (amends K4): station operator sessions.** `station_operator_sessions.user_id`
  gets K2's trigger.
- **K45 (amends K4): a key's queued work.** A job a key queues and that later runs
  `AsUser(created_by)` would fail, because `UserMayAct` is false for an integration.
  Delivery 0 lists the `AsUser` callers. Each one either keeps working for an
  integration, by acting as the job's own subject, or refuses the key's request up
  front with `api_not_available` through the operation's mark.
- **K46 (amends K28): the cache is bounded.** The filtered documents are kept in an LRU
  of 64 entries.
- **K47 (amends K3): which writes are guarded.** K3's trigger guards insert and update
  of `user_roles` and of every `access_group_memberships` row naming an integration.
  Deletes are not guarded: removing a role only narrows. Integrations are not given
  access-group memberships in this phase, so their scope is their direct roles.

- **K48 (amends K3), as built in delivery 0: a refused role write is 404.** Roles & access
  and the access-group member operations answer `404` for an integration, as the Owner's
  user operations do for an agent. A `409 not_a_person` would need a new response in
  `identity.yaml` for a user the screens never list anyway.
- **K49 (amends K4), as built: `UserMayAct` is person-only.** It already was, for agents as
  well (`queries/sessions.sql:85-112`), so `UserHolds` and `AsUser` answer false for
  every user who is not a person. `ListConnectCandidates`, which K4 did not name, also left
  out no kind; it now leaves integrations out. It still offers agents; that is the agents'
  question and is left as it was.
- **K50 (amends K4), as built: Knowledge keeps treating any `Via` as AI.** `viaAI`
  (`knowledge/server.go:42-51`) says on purpose that a kind of principal added later is
  covered without a change. A key therefore sees no hidden article. Narrowing it would
  open hidden articles to keys, and nobody has asked for that.
- **K51 (amends delivery 0's scope): `permitted` refuses `APINever` in delivery A.** The
  branch has no caller until a key principal exists, so it lands with the `Check` branch.
  Delivery 0 ships `contracts.APINever` and its test.
- **K52 (amends K6), as built: no Vipps or Storecove operation exists to mark.** The
  anonymous surface is calendar, communications' public media, Entra, Google, identity,
  the invoices pay page and Meta, every one marked `never`.

- **K53 (amends K6, K7), from delivery 0's reviews: what moves money or reveals personal
  data in bulk is `never` though its permission can be delegated.** Staff's personal data
  in clear and its writes, the payroll export and every account operation; Invoices'
  seller settings (its bank account), Vipps, the access point and the bank feed. Every
  operation naming an `APINever` permission is marked `never` too, which a test in
  `internal/integration` holds to the composed catalog (51 operations at the time). The
  owner may open any of these later, operation by operation.
- **K54 (amends K5): the router refuses a key behind a rule it never meets.** `Rule.API`
  is `APIRefused` for `session`, `policy` and `scim` operations whatever their mark, as
  the lint refuses `allowed` or `read` there.
- **K55 (amends K3): the role licence names one integration.** `vantigo.integration_roles`
  is set to the integration's id, and an access-group membership is refused for an
  integration even under it. More tables that name a user get 00141's guard: PINs,
  badges, assistant grants, expected links and the accounts they replace, password reset
  tokens and recovery codes.
- **K56 (for delivery A): a role an integration holds is not a steward's to widen.**
  A delegate who stewards a role could add permissions to it, which widens every key of
  an integration holding it. Delivery A decides between refusing a stewarded role to an
  integration and refusing a steward's edit of a role an integration holds. It also
  sweeps the record-level assignments (an owner, a member, an assignee) that a lookup by
  id would let an integration take.
- **K57 (amends K11): the display ends with the checksum.** `display` is `vtg_`, the
  first 4 random characters, `…` and the key's last 4 characters, which belong to the
  checksum, not to the random part.

## 15. Revision log

- **Revision 1, 2026-11-17.** First version, from R.
- **Revision 2, 2026-11-17.** After an adversarial review checked against the code.
  - **Critical:**
    - Integration roles get one writer, in code and in the database (K3). It closes a
      steward's path to widening a key.
    - `HasPermissions` re-runs `Check` with `APIAllowed` for keys (K5). Without it,
      handler-side checks would fail closed.
  - **Major:**
    - `APINever` and Staff's account operations marked `never` (K6, K7).
    - The kind sweep extended to the Go tests, directories, `managedTarget`,
      `UserMayAct`, the contracts' enums and Knowledge's `viaAI` (K4).
    - The read-only preset replaced by a read-only key enforced by operation (K5,
      K11).
    - Identity's first worker made explicit, with partitions created ahead and a
      DEFAULT (K15, K23).
    - The documentation table corrected to pages that exist and to `sources` coverage.
    - The lint requires explicit marks on anonymous operations and on identity, Google
      and Entra (K5, K6).
    - `/api/openapi.json` gets its own check, an effective-permissions query and the
      page's own key script (K27, K28).
    - Refused requests are attributed through a typed error and a recorder port (K23).
  - **Minor:**
    - An out-of-network request answers 401.
    - Several `Authorization` headers are refused.
    - `Via` carries `KeyID` only.
    - FKs are ON DELETE SET NULL, and `rolled_from` has a unique index.
    - The base62 padding is specified, and secret-bearing responses are `no-store`.
    - The rate limit is per integration, and `Decision` gains the header fields.
    - 401, 403 and 429 are declared in the integrator's document.
    - The resolver asserts the kind, and integrations get no built-in role.
    - The bundle overlay, with Dependabot bumping it.
    - The `!`-commit changelog.
    - The navigation item is `ownerOnly`, and no Assistant prefix is added.
    - The avatar gets an API badge.
    - Wrong cross-references and false claims about the repository are corrected:
      there is no Renovate, no identity worker, no `reference/identity.md` and no
      `admin/overview.md`; `cmd/contract` exists; a partition precedent exists.
- **Revision 3, 2026-11-17.** After the plan's review, §14 (K33 to K47) corrects:
  - the read-only key in handler-side checks;
  - the error wrapping;
  - the recorder and scope seams;
  - where the integrator's document is built, and the CI check;
  - the release notes;
  - the DEFAULT partition's order;
  - the browser tests;
  - the packaging;
  - the introduction page's path;
  - the avatar's package;
  - station operator sessions;
  - queued work;
  - the cache bound;
  - which role writes are guarded.

  Delivery 0 adds K48 to K56: what it built differently, and its reviews.
