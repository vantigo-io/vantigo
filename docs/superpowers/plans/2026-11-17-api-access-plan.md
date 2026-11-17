# API access — deliveries 0, A, B, C: implementation plan

Revision 2, 2026-11-17, after an adversarial review of revision 1. This plan builds the
first phase of API access: integrations, scoped API keys and the reference at `/api`.
It works from:

- **The spec:** `docs/superpowers/specs/2026-11-17-api-access-design.md`, revision 3.
  Its decisions are numbered K*n*, and its §14 (K33–K47) amends the earlier ones and
  wins where they differ.
- **The research:** `docs/superpowers/research/2026-11-17-api-keys-and-api-reference.md`
  ("R").

**Abbreviations:**

- `srv/` is `apps/server/internal/`;
- `mig/` is `srv/db/migrations/`;
- `host/` is `apps/host/frontend/src/`;
- `shell/` is `packages/frontend-shell/src/`;
- `cmd/` is `apps/server/cmd/vantigo/`.

## How it lands: one stack

The owner's eight questions (spec §13) stand at their defaults. Building starts when the
owner says so: this plan starts nothing by itself.

- **The tool:** `gh stack`, used only with its non-interactive flags.
- **Merging:** one layer lands per CI round through the merge queue.
  - The merge queue does not gate, so wait for the PR's own CI to be green before
    enqueueing (memory: merge-queue-does-not-gate).
  - Then retarget the next layer to `main` by hand, because the repository does not
    delete branches.
- **Each layer** is one PR. Its diff is that delivery and nothing else, and its body
  says which layer it sits on.

| # | Branch | Delivery | Migration (main at `a9f140c82`, latest `00192`) |
| --- | --- | --- | --- |
| 1 | `claude/vantigo-api-keys-docs-8b70ec` | Research, spec, plan, roadmap (docs only) | — |
| 2 | `claude/api-access-0-seams` | 0: the kind, its guards and sweep, `x-vantigo-api`, `APINever` | `00194_identity_integration_kind` |
| 3 | `claude/api-access-a-keys` | A: integrations, keys, `Check`, the screen, the worker | `00195_identity_api_keys` |
| 4 | `claude/api-access-b-log` | B: the request log, the limit, the notices | `00196_identity_api_requests` |
| 5 | `claude/api-access-c-reference` | C: `/api`, the integrator's document, the policy, `oasdiff` | — |

- **C does not depend on B.** It is stacked after B only because a stack is a line, and
  it can be moved under B.
- **Migration numbers.** `00193` is claimed by Invoices phase 6 (PR #368, `00193_invoices_branding`), so this stack starts at `00194`. If `main` takes a number first, the owning layer renumbers its
  migration in its own history before the stack is rebased (memory:
  migration-number-collisions). Check `git ls-tree origin/main` on `mig/` right before
  each merge.
- **The docs layer** changes only `docs/superpowers/**` and `ROADMAP.md`, so it needs no
  page.
- **The demo seed** grows in the layer that makes the thing: A adds the integration and
  its key, and B adds a day of request-log rows.

## How each delivery is run

The routine used for Purchasing, Accounting and Manufacturing.

1. **The seam first, by one agent.** The migration; the OpenAPI changes, each operation
   with a `summary`, its assistant mode and now its `x-vantigo-api` mark; `go generate`
   and sqlc; the contract types and the skeleton. All in one compiling commit.
2. **Lanes.** Each lane owns disjoint files, and no two agents work in one Go package at
   once:
   - `srv/identity`;
   - `srv/contracts` + `srv/module`;
   - the other modules' sweeps, one agent per batch of modules;
   - `shell/` and the host;
   - `apps/apiref/frontend`.
3. **Docs.** This feature touches many covered paths, so the docs step reads the map
   first.
   - Run `bun run tools/docs/check-coverage.ts --base <parent branch>` **before**
     writing, and update every page it names.
   - A one-line "never available to API keys" note on a module's `reference/<m>.md` is
     real documentation, and is preferred to a waiver.
   - Then run `go generate ./...` in `apps/server`, because the Assistant embeds the
     docs (memory: docs-edit-needs-go-generate).
4. **Review.** A code review of the server and the frontend, and for 0 and A a
   **security review** of the `Check` branch, the triggers and the sweep. Each runs in a
   separate pass. Fix what they find in the layer it belongs to.
5. **Gates,** in order:
   1. `bun install --frozen-lockfile` once in the worktree (memory:
      worktree-needs-bun-install).
   2. `mise run server:check`.
   3. `mise run server:test`. On SQLSTATE 53300, rerun the failing packages with
      `-p 1` (memory: shared-test-postgres-too-many-clients).
   4. `mise run frontend:check`. Read the log, not the exit line.
   5. `mise run docs:check` and the coverage check against the parent branch.
6. **Commits.** Signed Conventional Commits:
   - `feat(identity)`, `feat(contracts)`, `feat(module)`;
   - `feat(<module>)` for a module's sweep;
   - `feat(frontend-shell)`, `feat(host)`, `feat(apiref)`;
   - `ci`;
   - `docs(<module>)`;
   - `chore(assistant): regenerate the embedded documentation`.
7. **Next layer.** `gh stack add <branch>`, then `gh stack submit --auto`.

Workers are tested as `module.Workers` builds them, not through the harness's deps.

**Test fixtures and K3.** `modtest` writes `user_roles` directly (`srv/modtest/modtest.go:1272`,
`srv/modtest/assistant.go:115`). Delivery 0 adds `modtest.GrantIntegrationRoles(tx, …)`,
which sets `vantigo.integration_roles` in the same transaction. Every later test that
gives an integration a role uses it.

---

## Layer 1 — the design documents

R, the spec, this plan, and an **API access** section in `ROADMAP.md` under *Platform*,
after *Integrations*. The research and spec revision 2 are already in `1c40f68e7`. Spec
revision 3, this plan and the roadmap entry are committed as
`docs(api-access): plan, spec revision 3 and roadmap entry`.

## Layer 2 — delivery 0, the seams (K1–K7, K43–K45, K47)

### Step 0.1 — read before writing

The findings are recorded in the PR body. They are the authority for K4, K6 and K45.

- **Kind sites:**
  `git grep -n -e UserKindAgent -e UserKindPerson -e isAgent -e "kind = 'person'" -e "kind = 'agent'" -e "kind <> 'agent'" -- apps/server ':!*_test.go'`.
  Each site is classified as one of:
  - "means not a person": widen it;
  - "means an agent": keep it;
  - "a listing": exclude integrations.

  This covers at least `recovery.go:71` and `:105`, `delegations.go:209`,
  `users.go:86-102`, and `communications/conversations_notes.go:155, 174, 192`.
- **The directory pushes.** Read Entra's and Google's outbound user syncs, and name the
  queries they enumerate users with.
- **`SessionID` and `callerFrom`:**
  `git grep -n -e "SessionID" -e "callerFrom(" -- apps/server/internal ':!*_test.go'`,
  with each site mapped to its operationId (K6).
- **`AsUser` callers:** `git grep -n "AsUser(" -- apps/server/internal ':!*_test.go'`.
  For each job that a key's request could queue, decide whether it keeps working, or
  whether its enqueuing operation becomes `never` (K45).
- **POST searches** that only read. These become `read`.

### Step 0.2 — the migration `00194_identity_integration_kind`

- **The kind:**
  - `users.kind`'s CHECK gains `'integration'` (K1);
  - rename `users_agent_no_credentials` to `users_nonperson_no_credentials`.
- **The guards** (K2): `CREATE OR REPLACE` `identity.refuse_agent_user` and
  `identity.refuse_agent_privileged_role` with `kind <> 'person'`.
  - The trigger names stay.
  - `station_operator_sessions.user_id` gains the same trigger (K44).
- **The role writer** (K3, K47): `identity.refuse_integration_role_write()`, on insert
  and update of `user_roles` and `access_group_memberships`. It refuses a row naming an
  integration unless `current_setting('vantigo.integration_roles', true) = 'on'`.
- **The down migration:**
  - restore `00141`'s functions, the CHECK's name `users_agent_no_credentials` and the
    inline kind check;
  - drop the new triggers;
  - refuse while an integration row exists. Otherwise `00141`'s down migration would
    fail.

### Step 0.3 — contracts, module and lint

**`srv/contracts`:**

- the kind and the extension:
  - `UserKindIntegration`, and the `UserEntry.Kind` doc;
  - `APIExtension = "x-vantigo-api"` beside `StationExtension`;
- the rule and the principal:
  - `Rule.API`, with `APIRefused`, `APIAllowed` and `APIRead`;
  - `ViaAPIKey` and `Via.KeyID`;
- the refusals (K34):
  - `ErrAPINotAvailable` and `ErrAPIKeyReadOnly`, which wrap `ErrForbidden`;
  - `ErrAPIKeyExpired`, which wraps `ErrUnauthenticated`;
- `APINever(catalog []Permission) []string`: every `identity:*` and `agents:*` key,
  plus every key that is not Delegable (K7);
- `HasPermissions`: for a principal whose `Via.Kind == ViaAPIKey`, the inner `Check`
  carries `API: APIRead` (K33).

**`srv/openapi/lint.go`:**

- `allowed`, `read` and `never` are the only values;
- an explicit mark is required on every `anonymous` operation, and on every operation
  in `identity.yaml`, `google.yaml` and `entra.yaml`;
- the `never` list is pinned as a golden file in `srv/openapi/testdata`.

**`srv/module/router.go`:** `wrap` reads the mark into `Rule.API`.

- An absent mark means `APIAllowed`.
- A `GET` or `HEAD` with no mark is `APIRead`.

### Step 0.4 — identity (`srv/identity`)

- **Who counts as a person:** `managedTarget`, `delegations.go:209` and `recovery.go:71,
  105` test `!isPerson` (K4).
- **Role writes:** `PutIdentityAccessUsersByIdRoles` and the membership override refuse
  a target that is not a person with `409 not_a_person` (K3). Agents keep their own
  path, which step 0.1 confirms.
- **Queries:**
  - `ListAccessUsers` and `owner_users.sql` gain `kind <> 'integration'`;
  - `directory.sql`'s `include_agents` means `kind IN ('person','agent')`;
  - `UserMayAct`'s doc says it is for people only.
- **`permitted`:** refuses `APINever` keys for an `api_key` principal. The branch is
  dead until A, so it is tested with a constructed principal.
- **Outbound syncs:** Entra's and Google's leave integrations out (K4), with a test for
  each.

### Step 0.5 — the other modules, a commit each

- **The kind tests step 0.1 found:** `agents/roles.go:185, 194`,
  `agents/subject.go:236`, `agents/views.go:67`, and
  `communications/conversations_notes.go:155, 174, 192`.
- **`knowledge/server.go:42-51` `viaAI`:** narrowed to the `assistant` and `agent`
  kinds of `Via`. The PR body states that this also stops treating a `station` Via as
  AI, and that a station never reaches Knowledge.
- **The marks (K6):**
  - every `identity.yaml` operation: `never`, except the few the plan finds a key
    could meet (none expected);
  - `google.yaml` and `entra.yaml`: every operation marked, with the connect flows
    `never`;
  - `meta.yaml`, Vipps, Storecove and the calendar feed: `never`;
  - Staff's account and pass operations: `never`;
  - Assistant and agents conversations and runs: `never`;
  - POST searches: `read`;
  - the operations K45 names.
- **`shell/ui/user-avatar.tsx`:** an API badge for `kind === "integration"` (K43),
  with English and Norwegian strings in the shell's catalog.

### Step 0.6 — tests

- **Migration:** with an integration user, the database refuses:
  - a session, a passkey, an invitation, an OIDC link and a SCIM mapping;
  - a delegation and a steward;
  - the Owner and SystemAdmin roles;
  - a station operator session.

  A `user_roles` insert fails without the setting and succeeds with it. The down
  migration works on a database without integrations, and refuses on one with
  integrations.
- **Identity:**
  - the listings, with all three kinds present;
  - `managedTarget`;
  - `409 not_a_person` on both paths;
  - the outbound syncs.
- **The lint:**
  - an anonymous operation without a mark fails;
  - the golden `never` list matches.
- **`APINever`:** computed from the composed catalog. A test fails when a new
  `identity:`, `agents:` or non-Delegable key is missing from it. It also asserts that
  no `allowed` operation names only `APINever` keys (K7).
- **`HasPermissions`:** for an `api_key` principal, the inner rule carries `APIRead`.
  A fake `Access` records the rule (K33).
- **The avatar:** the badge renders for `integration` (vitest).

### Step 0.7 — docs

Run the coverage map first. Expect at least these pages.

- **Contributing (en only):**
  - `contributing/module-boundaries.md`: `x-vantigo-api` among the extensions, and the
    rule that a non-Delegable permission is never available to keys;
  - `contributing/documentation.md`: the extension in the lint;
  - `contributing/frontend-architecture.md`: the avatar's kinds.
- **`admin/authentication.md` (en + nb):**
  - that a third kind of account exists for integrations, and how it differs from
    people and agents;
  - that it can hold no session, Owner role or SystemAdmin role.

  Do not write "arrives in a later release": the docs publish on every push to `main`.
  Write the page as true at this commit.
- **The reference pages of every swept contract:** `reference/<m>.md` for Google,
  Entra, Meta, Staff, the Assistant, Agents, Calendar, Invoices (Vipps, Storecove), and
  each contract with an anonymous operation or a POST search.
  - Each gets a line saying which of its operations are never available to API keys,
    or which count as reads.
  - Agents' changes also touch `admin/agents.md` and `user/agents.md` (en + nb), if the
    map names them.
- **Knowledge and Communications:** `reference/knowledge.md` (the AI check), and
  `reference/communications.md`.

## Layer 3 — delivery A, integrations and keys (K8–K22a, K34, K36)

### Step A.1 — the seam

- **Migration `00195_identity_api_keys`:**
  - `identity.integrations` (K8):
    - a partial unique index on `lower(name)` where `archived_at IS NULL`;
    - `CHECK (rate_limit_per_minute BETWEEN 60 AND 6000)`;
    - the FKs: people's ON DELETE SET NULL, and `user_id` RESTRICT.
  - `identity.api_keys` (K11):
    - a unique `token_hash`;
    - a unique partial index on `rolled_from`;
    - a partial index on `expires_at` where `revoked_at IS NULL`;
    - CHECKs on `cardinality(allowed_networks) <= 20` and on the name length.
- **`openapi/identity.yaml`:** K21's endpoints, each:
  - `policy:Owner+OwnerManagement`, `x-vantigo-api: never`,
    `x-vantigo-assistant: never`;
  - `If-Match` on `PATCH`;
  - the secret-bearing responses.
- **Queries** (`queries/integrations.sql`, `queries/api_keys.sql`), with the
  effective-permissions query `EffectivePermissionsForUser` (roles and access groups);
  sqlc; the generated server.

### Step A.2 — the key and the branch

- **`apikey.go`:** the K11 format, with base62 left-padded to 43 characters and the
  CRC32 left-padded to 6. It provides `newAPIKey`, `parseAPIKey` and `display`, and
  stores with `hashToken`.
- **`access_apikey.go`, `checkAPIKey`:**
  - the single-header rule (K16);
  - one read that also asserts `kind = 'integration'` and reads
    `rate_limit_per_minute` for B (K17);
  - the networks (401, reason `network`, no `last_used_*` update; K14);
  - expiry (`ErrAPIKeyExpired`);
  - `Rule.API` and read-only (`ErrAPINotAvailable`, `ErrAPIKeyReadOnly`; K9, K34);
  - the rule-kind table;
  - `evaluate` with an integration subject;
  - the `last_used_*` write, throttled to once a minute.

  Every refusal is returned as `contracts.APIKeyRefusal{KeyID, IntegrationID, Reason,
  Err}`. Its `Unwrap` reaches K34's sentinels, so `wrap`, `compose.go` and the live
  handler answer it as 401 or 403, not 500.
- **`Access.Check`:** the branch after SCIM, before stations. A key request reads no
  cookie.
- **`Access.Reject`:**
  - leaves the cookie for a key request (K18);
  - branches on each sentinel to write K19's code;
  - sets `WWW-Authenticate` on a key's 401.
- **`Access.Scope(ctx, Principal)`** implements `contracts.APIKeyScope` (K36): the
  effective permissions minus `APINever`, intersected with the narrowing, and the
  read-only flag. C reads it.
- **The live socket:** a test shows it refuses a key (K20).

### Step A.3 — the service and the endpoints (`integrations.go`)

- **Create.** The user row gets the address `integration-<uuid>@integrations.invalid`,
  kind `integration`, and no built-in role (K1).
- **The other integration writes:** update, roles, disable, enable and archive.
  - Every role write runs in a transaction that sets
    `set_config('vantigo.integration_roles','on',true)`.
  - Archiving revokes every key in the same transaction.
- **Keys:**
  - create: expiry ≤ 365 days, the narrowing, read-only, and networks ≤ 20 CIDRs;
  - roll: an overlap ≤ 7 days, and `409 api_key_already_rolled` from the unique index;
  - revoke.

  Secret-bearing responses carry `Cache-Control: no-store`.
- **`GET /{id}`:** returns the effective permissions, and for each name in each key's
  narrowing whether the roles grant it and whether the catalog still has it (K11's
  marks).
- **Audit** (K22a): each write calls `writeAudit` on its own transaction.

### Step A.4 — the worker (`srv/identity/workers.go`)

- **Registration.** `identity.api-keys` is registered in `Module.Workers`. It follows
  `srv/assistant/workers.go:127-160`: `retentionWorker.RunCycle` →
  `expireUnderLease`, with `pg_try_advisory_lock` and its own lease key.
- **Configuration.** `IDENTITY_API_KEYS_POLL` (default `1h`) and
  `IDENTITY_API_KEYS_ENABLED` (default `true`), in `srv/config` beside the other
  workers' settings.
- **This delivery's work:** record `api_key.expired`, and delete key rows 90 days past
  expiry (K12, K15).

### Step A.5 — the host

**`host/routes/workspace/api-access.tsx`** and its children:

- **The list.**
- **An integration's page:**
  - its roles, with *Roles & access*' picker, lifted into a shared component if it is
    local to that page;
  - its effective permissions, with `APINever` marked;
  - its keys, with K11's marks on each narrowing.
- **The key dialog:**
  - the narrowing checklist, the *Read only* switch, the networks and the expiry;
  - the one-time secret, with *Copy*, cleared on close;
  - a `curl` example from `APP_URL`.

**Around the page:**

- `host/apps.ts`: the navigation item beside *Stations*, `ownerOnly: true`; the
  spotlight entry; `i18n.ts` in English and Norwegian;
- Sensitive badges (K22);
- the client goes through `"@vantigo/app"`, so `sessionClients` needs no new entry.

### Step A.6 — tests

- **`apikey_test.go`:** the format, the padding, the checksum rejecting look-alikes,
  and round trips.
- **`access_apikey_test.go`:**
  - K9's matrix: one operation per rule kind × {full, narrowed, read-only};
  - `APINever`;
  - every K19 code, through `wrap`, asserting 401/403 and never 500 (K34);
  - cookie precedence, and `Reject` leaving the cookie;
  - several `Authorization` headers;
  - disabled, archived, revoked and expired keys;
  - networks behind a trusted proxy.
- **The whole contract** (`srv/integration/api_key_scope_test.go`). This copies
  `TestStation_TheWholeContract` (`srv/integration/station_scope_test.go:36-100`):
  - every mounted operation is called with an empty JSON body and random path ids;
  - one integration holds a role with every Delegable, non-`APINever` key;
  - assert the answer is neither 401 nor 500;
  - an operation that names only `APINever` keys answers 403, and an operation marked
    `never` answers `api_not_available`;
  - require at least 1,000 operations walked;
  - skip, by an allow-list in the test, the operations that call an external provider:
    Bring, Folio, Graph, Google, Meta, Peppol and AI;
  - budget: the station walk's runtime ×1.5. Shard it if it exceeds that.
- **Handler-side checks** (K33). Start from `git grep -n "HasPermission" -- apps/server/internal`.
  For each module with a handler-gated field or conditional write, a key holding the
  permission gets what a person gets, **including a read-only key on a `GET`**.
  Examples: accounting `permClose` (`vouchers.go:439`), knowledge `canManage`.
- **Secrets.** Create, roll and use a key, then assert that no captured log line and no
  span attribute contains `vtg_`. This needs a small harness in `srv/modtest`: a
  capturing `slog` handler and the `tracetest` in-memory exporter. Neither exists there
  today.
- **The worker:** an expired key is audited once, and a key 90 days past expiry is
  deleted.
- **The host** (vitest):
  - the dialog shows the secret once, and clears it on close;
  - `APINever` is marked;
  - `ownerOnly` hides the item;
  - the narrowing marks.

### Step A.7 — the seed (`cmd/seed*.go`)

- **"Webshop sync"** gets a custom role, "Webshop", with these catalog keys:
  - `customers:view`, `customers:create` and `customers:update`;
  - `products:products-view` and `products:pricing-view`;
  - `orders:access`.

  Check each key against the catalog when the step runs.
- **One key:** 90 days, not narrowed.
- **Idempotent.** A second seed run leaves the integration as it is and makes no second
  key.
- **The secret** is printed once by the seed command. It is never written to a file,
  and never printed when the seed runs against a hosted demo (`APP_URL` not on
  localhost). There the Owner makes a key in the screen.
- **The seed test** asserts the integration and the role, never the secret.

### Step A.8 — docs

Run the coverage map first. Expect these pages.

- **`admin/api-access.md`** (en + nb), new. It covers:
  - the preconditions;
  - creating an integration and giving it roles;
  - making a key, narrowing it, making it read-only, and restricting its networks;
  - rolling and revoking a key;
  - what "never available to API keys" means;
  - what to do when a key leaks.

  Its `sources`: `srv/identity/integrations.go`, `srv/identity/access_apikey.go`,
  `srv/identity/apikey.go` and `host/routes/workspace/api-access.tsx`.
- **`admin/index.md`** (en + nb): list the page.
- **`admin/authentication.md`** (en + nb): keys as a way to authenticate, K9's table,
  K19's codes, and the audit actions.
- **`admin/configuration.md`** (en + nb): the worker, `IDENTITY_API_KEYS_POLL` and
  `IDENTITY_API_KEYS_ENABLED`.
- **`user/workspace-administration.md`** (en + nb): the *API access* screen, as a task.
- **`contributing/module-boundaries.md`:** `APIKeyRefusal`, `APIKeyScope`, and the
  sentinels.
- **The embedded docs:** `go generate`, then the `chore(assistant)` commit.

## Layer 4 — delivery B, the request log, the limit and the notices (K23–K25, K35, K39)

### Step B.0 — measure first

On the test database, drive 600 requests a minute from one integration with 8
concurrent clients, through `ratelimit` keyed by the integration.

- Record the p50 and p99 added latency in the PR body, with the caveat that production
  contention may differ.
- If p99 exceeds 5 ms, build K24's in-process pre-count first, flushed every second.

### Step B.1 — the seam

- **Migration `00196_identity_api_requests`:**
  - `identity.api_requests`, partitioned by range on `at`, following
    `mig/00005_energy_baseline.sql:122-160`. It has no FK to `api_keys` (K39);
  - `identity.ensure_api_requests_partition(day date)`, which does K39's
    drain-create-reinsert in one transaction;
  - the next 7 days, created by the migration;
  - a DEFAULT partition;
  - the index `(key_id, at desc)`;
  - `identity.api_key_notices`, keyed by `(key_id, kind)`.
- **`contracts.APIRequestRecorder`:** `Record(ctx, APIRequest)`. Identity's `Access`
  implements it (K35).
- **`ratelimit.Decision`** gains `Remaining` and `Reset`. Update the callers and tests.
- **`openapi/identity.yaml`:** `GET /integrations/{id}/requests`, filtered by key,
  status class, reason and day.

### Step B.2 — the router (`srv/module/router.go`)

- **The recorder:** `rec, _ := r.opts.Access.(contracts.APIRequestRecorder)` (K35).
- **After `Check` succeeds for a key principal:**
  - the `api` policy, keyed by the integration, with its limit read from the
    principal (K17);
  - the `RateLimit-*` headers;
  - a 429 through `ratelimit.Reject`.
- **Status capture:** export `httpx`'s `statusRecorder` (`srv/httpx/log.go:45`), keeping
  its `Unwrap`, so streaming still works. `wrap` uses it to know the status.
- **Recording:** after the handler, `rec.Record` with the operationId, the status, the
  duration, the client IP and the trace id. On a refusal, `errors.As(err, &refusal)`
  records the reason. An unknown key is not recorded.

### Step B.3 — identity

- **`apirequests.go`, the buffered writer:**
  - it flushes every 2 seconds, every 500 rows, and on `Close`;
  - it uses its own pooled connection;
  - when full it drops rows, counted in a metric.

  It is started and closed where `identity.NewAccess` is built (`cmd/main.go:315`),
  for the API process only.
- **The worker, extended:**
  - for each of the next 7 days that is missing, `ensure_api_requests_partition`
    (K39);
  - drop partitions older than 30 days;
  - send K25's notices through the invitation mailer, in the recipient's language,
    idempotently through `api_key_notices`.
- **The log endpoint:** per-day counts by status class, the top operations, and the
  latest 200.

### Step B.4 — the host

- **The *Requests* tab** on the integration's page:
  - daily counts by status class;
  - the top operations;
  - the latest requests;
  - filters.
- **Strings** in English and Norwegian.

### Step B.5 — tests

- **The writer:** it flushes on size, on time and on `Close`. A slow database never
  blocks a request. A full buffer drops rows and counts them.
- **Partitions:** a row with no partition for its day lands in DEFAULT. `ensure` then
  drains it, creates the day and reinserts the row, which runs cleanly twice in a row.
  An old partition is dropped. With the worker off, rows still land in DEFAULT.
- **Refusals:** expired, network, read-only and never are each recorded with their
  reason. An unknown key is not recorded. No row contains `vtg_`.
- **The limit:** the 601st request in a minute, across two keys of one integration,
  answers 429 with the headers. Two integrations count separately.
- **Notices:** 14 days, 3 days and the day itself are each sent once. A rolled key gets
  none. A disabled contact is skipped, and the Owners still get the notice.
- **The seed:** a day of request rows, idempotent.

### Step B.6 — docs

Run the coverage map first.

- **`admin/api-access.md`** (en + nb): the request log, the limit and its headers, and
  the notices.
- **`admin/authentication.md`** (en + nb): `rate_limited`, and the log as the record of
  what a key did.
- **`admin/configuration.md`** (en + nb): what the worker does now, and what switching
  it off means (K39).
- **`contributing/module-boundaries.md`:** `APIRequestRecorder`, found by type
  assertion.
- **`contributing/`'s page on `httpx`,** if the map names one: the exported status
  recorder.
- **The embedded docs:** `go generate`, then the `chore(assistant)` commit.

## Layer 5 — delivery C, the reference at /api (K26–K32, K36–K42, K46)

### Step C.0 — verify Scalar under the policy

Before building anything:

1. Build a throwaway page from the pinned `@scalar/api-reference` standalone bundle.
2. Serve it with K27's CSP and load a document.
3. Record in the PR body:
   - whether it needs `unsafe-eval`;
   - which external requests it attempts with `withDefaultFonts: false`, no
     `proxyUrl`, telemetry off and "open in Scalar" off.

If it needs `unsafe-eval`, the page's CSP alone gains it, and the spec gets an
amendment.

### Step C.1 — `apps/apiref/frontend` (K41)

- **The package.** A bun workspace package (it matches `apps/*/frontend`), with
  `@scalar/api-reference` pinned. A Vite build produces `index.html`, `apiref.js` and
  Scalar's bundle.
- **`apiref.js`:**
  - the key field;
  - on submit, `fetch('/api/openapi.json', {headers: {Authorization: 'Bearer …'}})`,
    then mount Scalar with the document as `content` and the bearer token;
  - Scalar options: `persistAuth: false`, `withDefaultFonts: false`, telemetry off,
    "open in Scalar" off, no `proxyUrl`;
  - clearing the field remounts without the key.
- **Without a key and without a session,** the page shows the explanation and links to
  `https://docs.vantigo.io/en/reference/api-introduction/`.
- **What a signed-in person sees** (K29): the `?view=api` document, and the notice that
  "try it" without a key uses their own session.
- **Copy** in English and Norwegian, by `navigator.language`.
- **Embedding:**
  - `scripts/spa-embed-overlay.sh` gains the build into `srv/apiref/dist`;
  - `scripts/restore-embed-overlay.sh`'s `EMBED_DIRS` gains `srv/apiref/dist`;
  - `srv/apiref/dist/index.html` is a committed placeholder, as `srv/docs`'s is.

  `build-artifacts.sh` and goreleaser's before hook already run the overlay.
- **Notices.** Scalar's MIT licence joins the third-party notices.

### Step C.2 — the server

- **`srv/apiref/handler.go`:**
  - `GET` and `HEAD /api` serve `index.html` when `Accept` prefers `text/html`, and the
    existing 404 otherwise;
  - `GET /api/reference/{file}` serves the assets with immutable caching;
  - K27's CSP.
- **Depguard.** `srv/apiref` joins the platform list in `apps/server/.golangci.yml`.
- **The mount** is in `compose.go`, before the `/api` NotFound, or matched in
  `server.go` ahead of `apiHandler`. Pick whichever keeps the 404 contract test
  passing unchanged.
- **The public host:** a test shows `/api` is refused there.
- **`srv/openapi/integrator.go`, `Integrator(contract, catalog, filter)`** (K37):
  - **The operations kept:**
    - the rule is a permission or anonymous;
    - `Rule.API` is not refused;
    - the operation does not name only `APINever` keys, computed from the `catalog`
      parameter.
  - **The filter** (K28), when given: the operation's permissions must be within the
    given set; read-only keeps `read` operations only; anonymous `allowed` operations
    are always kept.
  - **Added:**
    - `bearerAuth`, with a top-level `security`, and `security: []` on anonymous
      operations;
    - `servers` set to `APP_URL`;
    - 401, 403 and 429 on every operation, with K19's codes;
    - "*Requires …*" in each description;
    - the `info.description` links.
  - **Stripped:** the `x-vantigo-*` extensions.
- **`/api/openapi.json`** (`compose.go:663-676`):
  - its own `Check` with `Rule{Kind: RuleAnonymous, API: APIAllowed}`; a principal with
    no user answers 401;
  - a session with no parameter gets today's document;
  - a session with `?view=api` gets `Integrator` without a filter;
  - a key gets `Integrator` filtered by `Access.(contracts.APIKeyScope).Scope` (K36);
  - filtered documents are cached in an LRU of 64 entries (K46).

### Step C.3 — the policy and the check

- **`vantigo openapi integrator`** in `cmd/` (K37) composes every module and writes
  `Integrator(contract, catalog, nil)` to stdout.
- **CI** (`.github/workflows/ci.yml`): a step in `lint_server`, **on `pull_request`
  only**. It is skipped on `merge_group` and `push`.
  1. Fetch the base: `git fetch --depth=1 origin ${{ github.event.pull_request.base.sha }}`,
     then `git worktree add ../base FETCH_HEAD`.
  2. If the base has no `openapi integrator` subcommand, skip. This covers the first
     PR.
  3. Build both documents, using the Go build cache. Cache the base document by base
     sha.
  4. Run `oasdiff breaking base.json head.json --fail-on ERR`.
  5. On a finding, `tools/api/breaking-check.ts` reads the PR's commits. Each failing
     operationId needs a commit whose subject has `!:` and whose `API-Breaking:`
     trailer names it. The script then adds the `breaking-api` label.
- **Tooling.**
  - Pin `aqua:oasdiff/oasdiff` in `mise.toml`, and add it to the mise action's
    `install_args`.
  - Add `tools/api` to the test script that runs `tools/openapi` (`gen:client:test`).
- **Release notes:** `.github/release.yml` gains a *Breaking API changes* category on
  the `breaking-api` label (K38).

### Step C.4 — tests

- **Go:**
  - `/api`: HTML for `Accept: text/html` and a 404 otherwise; the CSP header; the
    assets; refusal on the public host.
  - `/api/openapi.json`: no caller is refused with 401. The other cases (a session, a
    session with `?view=api`, a narrowed key, a read-only key) each validate with
    kin-openapi and declare 401, 403 and 429.
  - The cache is bounded.
  - `Integrator`: golden files for a small composed contract, covering the filters,
    `security`, `servers`, the responses and the stripped extensions.
- **vitest** (`apps/apiref/frontend`, jsdom):
  - `apiref.js` sends the key in the header;
  - it writes nothing to `localStorage` or `sessionStorage`;
  - it remounts on clear (K40).
- **`tools/api/breaking-check.test.ts`:** a removed field fails, and the trailer
  passes.

### Step C.5 — docs

Run the coverage map first.

- **`admin/installation.md` and `admin/transport-security.md`** (en + nb): `/api`,
  `/api/openapi.json` admitting a key, and `?view=api`.
- **`admin/api-access.md`** (en + nb): "Trying the API at /api".
- **`reference/api-compatibility.md`** (en), new (K30).
- **`reference/api-introduction.md`** (en), new (K42), linked from the sidebar beside
  the generated reference. Its `sources` name `srv/apiref` and `apps/apiref/frontend`.
- **`docs/astro.config.mjs`:** add the Agents and Meta contracts.
- **`contributing/documentation.md`:** those contracts, `x-vantigo-api`, the
  `API-Breaking` trailer and the `breaking-api` label.
- **`contributing/module-boundaries.md`:** `compose.go`'s new `/api` handling.
- **`CONTRIBUTING.md:399-404`:** `/api/openapi.json` admits a key and takes
  `?view=api`, and `/api` itself serves the page.
- **The embedded docs:** `go generate`, then the `chore(assistant)` commit.

## The public site

Once A and C have merged, open a pull request in `vantigo-landing`:

- an **"API and integrations"** section on the front page, written from
  `user/workspace-administration.md` and `admin/api-access.md`;
- run `mise run apps:check` there;
- link the two pull requests.

If the landing repository is not available, say so in the hand-over and name what the
section must say (AGENTS.md, *The public site*).

## After the stack

- **The browser check,** with two profiles (K40): an Owner who makes a key, and a
  terminal that uses it.
  - Make, narrow, roll and revoke a key, and read the log.
  - At `/api`: try a call, and confirm in the network panel that no request leaves the
    origin.
  - Confirm that the Owner's session survives a bad key.
- **Update memory** with the state and the lessons.
- **The next research:** outgoing webhooks (spec §12), then MCP.

## Revision log

- **Revision 1, 2026-11-17.** First version.
- **Revision 2, 2026-11-17.** After the plan's review:
  - **Seams and filters:**
    - the integrator's document is built in `cmd/` from the composed catalog;
    - the inner check is `APIRead`;
    - the refusals wrap the sentinels;
    - the recorder and the scope are found by type assertion;
    - the DEFAULT partition is drained before a day is created.
  - **Tests:**
    - no Playwright;
    - the whole-contract walk copies the station walk;
    - a log and trace capture harness;
    - `modtest`'s role helper.
  - **Docs:** every layer reads the coverage map first, and the reference pages of the
    swept contracts are named.
  - **Packaging:** `apps/apiref/frontend`, the restore script, the MIT notice and
    depguard.
  - **CI and release:** the CI check runs on pull requests with the base fetched and
    `oasdiff` installed; release notes come through `.github/release.yml`.
  - **Smaller fixes:**
    - the seed's keys exist and the seed is idempotent;
    - the grep catches `isAgent` and `UserKindPerson`;
    - the avatar is the shell's;
    - the down migration restores `00141`'s names;
    - the coverage gaps the review listed are closed.
