# Google Workspace, delivery A — the module's foundation: implementation plan

> **For agentic workers:** implement this plan task by task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** an Owner adds `google` to `MODULES` beside `staff`, opens **Workspace admin ›
Integrations › Google Workspace**, uploads the service account's JSON key, names the
admin to impersonate, enters the Internal web client, and tests the connection: the page
lists the scopes to delegate, then which are granted, missing or unexpected, the
customer id, the organisation's name and its verified domains. Nothing else in Vantigo
changes.

**Architecture:** a new business module `google` (`srv/google`, schema `google`,
`openapi/google.yaml`, `@vantigo/google-ui`) in Entra's shape, filling no slot. A
guarded client (`client.go`) reaches a fixed list of Google hosts through netguard's
dialler; `token.go` mints domain-wide delegation tokens with
`golang.org/x/oauth2/jwt.Config`, the guarded client handed in the context as
`oauth2.HTTPClient`, cached in memory per `(credential, subject, scopes)`.
`tokenSource(scopes, subject)` is the one seam every caller uses. *Test connection*
asks one token per needed scope and one per scope Vantigo never uses, then reads
`customers.get(my_customer)` and `domains.list`. `googletest` fakes the token endpoint
(verifying the JWT against the uploaded key) and the two Directory calls.

**Spec:** [`docs/superpowers/specs/2026-11-03-google-workspace-design.md`](../specs/2026-11-03-google-workspace-design.md), G12–G16 and P1–P5.
**Brief:** [`2026-11-03-google-workspace-briefs/a-foundation.md`](2026-11-03-google-workspace-briefs/a-foundation.md).

## Plan readings

Checked against `3eca9ade` (delivery 0's branch, `feat/google-neutral-seams`).

1. **Migrations**: `main`'s latest is `00108_inventory_baseline.sql`; delivery 0 took
   00109–00111. This delivery's is `00112_google_baseline.sql`.
2. **The module lists** are `openapi.Modules` (`srv/openapi/openapi.go:25`, which
   `config.knownModules` and `defaultModules` derive from), `moduleSchemas`
   (`srv/db/schema_test.go:28`), the `Modules` literal in `srv/module/compose_test.go:617`,
   `config_test.go`'s default and known lists, `businessModules` in
   `cmd/vantigo/main.go:329`. `google` goes after `entra` in each.
3. **`google requires staff`** sits beside `entra requires staff`
   (`srv/config/config.go:2127`), with a test beside Entra's in `config_test.go:996`.
4. **depguard**: one rule set per module in `apps/server/.golangci.yml`; `google` joins
   every other module's and the platform's deny lists, and gets its own denying every
   other module; `google/googletest` joins `test-fakes`.
5. **`golang.org/x/oauth2` v0.37.0** is a direct dependency already. `jwt.Config`'s
   `jwtSource.Token` fetches through `oauth2.NewClient(ctx, nil)`, i.e. the context's
   `oauth2.HTTPClient`, and answers a refusal as `*oauth2.RetrieveError` whose `Error()`
   carries Google's body: the module reads the OAuth `error` code from `Body` itself and
   never logs or wraps the `RetrieveError`.
6. **The host**: `ModuleWithApp` excludes `entra` (`host/apps.ts:73`), with a comment
   saying Google joins it; the Integrations section (`host/apps.ts:637`) and the account
   menu (`host/account-menu.ts:37`) list Entra's item; `routes/workspace.tsx:24` admits a
   holder of `entra:manage` to the workspace layout; `sessionClients`
   (`host/api/session-handlers.ts`); the catalogs `catalogs/navigation.ts` and
   `catalogs/admin.ts` (one entry per permission).
7. **The redirect URIs** the Connection tab shows are D's callback,
   `APP_URL/api/v1/google/connect/callback`, and identity's OIDC callback,
   `APP_URL/api/v1/identity/oidc/callback` (`srv/identity/oidc.go:82`), both with the
   base path.
8. **Errors at the token endpoint.** A scope not delegated answers `unauthorized_client`
   (U14; `access_denied` treated the same) and is a *missing* scope; `invalid_grant` is
   the key refused (`errCredentialInvalid`), which also covers an `admin_subject` Google
   does not know — the guide says so under *What can go wrong*.

## Decisions taken in planning

- **Status.** `unconfigured` while no live key or no `admin_subject`, else the row's
  status, which the test writes. Changing `admin_subject` sends the row back to
  `unconfigured` and clears the scopes' lists (the last test was another subject's).
- **`PUT /google/connection/oauth-client`** takes `{clientId, clientSecret?}`: a changed
  client id needs a secret (400 otherwise); a new secret revokes the older live one. It
  moves the connection's version, as a configured value, but takes no `If-Match`.
- **Reasons**: `not_delegated` (pending), `credential_invalid`, `admin_refused`,
  `google_unreachable` (failing).
- **Events** carry codes only, never an address: the key's row id, its key id and the
  service account's numeric client id; `admin_subject_changed` without the address.

## Tasks

- [ ] **1. Plan** — this file, `docs(google): the plan for the foundation (Google A)`.
- [ ] **2. The module** — `feat(google): the module, the connection, the key and the guarded client (Google A)`.
  Migration `00112_google_baseline.sql` as G12; `openapi/google.yaml` with A's seven
  operations and their `x-vantigo-access`; codegen config, generate lines, `sqlc.yaml`,
  queries; depguard; `moduleSchemas`, `openapi.Modules`, `config` (`google requires
  staff`) and its tests, `compose_test.go`, `businessModules`. `client.go` (hosts,
  netguard, budgets, one retry, caps, codes only), `token.go` (`jwt.Config`, the
  cache, `tokenSource`), `credentials.go` (G13), `connection.go` (GET/PUT, the
  oauth-client, the test, G15), `events.go`, `errors.go`, `status.go`. `googletest`
  (token endpoint verifying the JWT, `customers.get`, `domains.list`). Tests: access
  of every operation, the key's rules, rotation, `client_mismatch`,
  `credential_kind_unsupported`, the test's three states and unexpected scopes, the
  client's hosts, retry and caps, words never logged, `contracttest.RequireCoverage`.
- [ ] **3. The frontend package** — `feat(google-ui): the Google Workspace page's four tabs (Google A)`.
  `apps/google/frontend` as `@vantigo/google-ui`: the API client and schema
  (`gen-client`), the Status and Connection tabs, the two placeholder tabs, both
  languages; the `no-restricted-imports` block copied, `google` on every other
  package's deny list.
- [ ] **4. The host** — `feat(host): Google Workspace under Integrations (Google A)`.
  `/workspace/google` and its four tab routes; the Integrations item and the account
  menu's; the workspace layout admitting `google:manage`; `moduleKeys`,
  `ModuleWithApp`; the catalogs (navigation, admin's permission label) in en and nb;
  `sessionClients`; the spotlight and app tests' lists.
- [ ] **5. Documentation** — `docs(google): …`: new `admin/google-workspace.md` and
  `user/google-workspace.md` (en, nb), new `reference/google.md`; the section indexes;
  `admin/configuration.md`, `user/workspace-administration.md` (en, nb);
  `contributing/module-boundaries.md`; `ROADMAP.md`; the module lists in `README.md`,
  `CONTRIBUTING.md`, `deploy/compose/` and `admin/installation.md` (both); the API
  reference in `docs/astro.config.mjs`.
- [ ] **6. Embedded docs** — `go generate ./...` in `apps/server`,
  `chore(assistant): regenerate the embedded documentation`.
- [ ] **7. Gates** — `mise run server:check`, `mise run server:test`,
  `mise run frontend:check` (its log read), `mise run docs:check`, serially.

## Unverified items this delivery depends on

- **U2** (new organisations enforce `iam.disableServiceAccountKeyCreation`):
  documentation only — the guide covers lifting it for the project.
- **U14** (a scope not delegated answers `unauthorized_client`): `access_denied` is
  treated the same; any other refusal is the key's or Google's, never a missing scope.
