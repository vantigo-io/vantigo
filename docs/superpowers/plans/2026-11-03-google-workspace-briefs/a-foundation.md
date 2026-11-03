# Brief A — The Google Workspace module's foundation

You are implementing delivery **A** of the design
`docs/superpowers/specs/2026-11-03-google-workspace-design.md` (decisions **G12–G16**).
Read that file first, in full, then this brief. You work alone, from a cold start, in
this repository. Model: Claude Sonnet. Delivery 0 is on `main`.

## The outcome

An Owner adds `google` to `MODULES` (beside `staff`), opens **Workspace admin ›
Integrations › Google Workspace**, and in the **Connection** tab uploads the service
account's JSON key, names the admin to impersonate, enters the Internal web client's id
and secret, and clicks **Test connection**. The page lists the scopes to paste into the
Admin console's domain-wide delegation, then shows which are granted, missing or
unexpected, the customer id, the organisation's name and its verified domains. The
**Status** tab shows the state and the events. **Accounts** and **Calendar and Gmail**
exist as routes with one sentence each about what comes. Nothing else in Vantigo
changes.

## Read before you plan

- `AGENTS.md`, `CONTRIBUTING.md`, `en/contributing/module-boundaries.md` (*Adding a
  module* at about line 1534, every step), `en/contributing/platform-packages.md`
  (`netguard`, `secrets`).
- The Entra module as the template: `apps/server/internal/entra/{module.go,graph.go,graph_token.go,credentials.go,connection.go,status.go,events.go,errors.go}`,
  `entra/graphtest/`, `mig/00061_entra_baseline.sql`, `openapi/entra.yaml` (the
  connection, credentials, test and events operations and their `x-vantigo-access`),
  `apps/entra/frontend/src/pages/{status-tab,connection-tab}.tsx` and its components,
  `apps/host/frontend/src/routes/workspace/entra/` and `routes/workspace/entra.tsx`.
- `apps/server/internal/secrets/secrets.go` (purposes), `internal/netguard/netguard.go`,
  `internal/config/config.go` (`knownModules`, the requirement checks near `:2127`),
  `internal/db/schema_test.go:28`, `internal/openapi/openapi.go:25`,
  `apps/server/generate.go`, `apps/server/.golangci.yml`, `cmd/vantigo/main.go:329`.
- Host: `apps/host/frontend/src/{apps.ts,navigation.ts,i18n.ts,catalogs/admin.ts,api/session-handlers.ts}`.
- Docs: `en/admin/entra.md` and `en/user/entra.md` (the shape of the pages you write),
  `en/admin/configuration.md`, `en/user/workspace-administration.md` (*Integrations*),
  the three section indexes.

## Build

1. **The module skeleton**, every step of *Adding a module*: `internal/google` with
   `Module()` (name `google`, permission `google:manage` as G16, no slots yet),
   `openapi/google.yaml`, codegen config and generate lines, `sqlc.yaml`, depguard rules
   (and `google` on every other module's deny list), `moduleSchemas`, `knownModules`
   and **`google requires staff`** with its `config_test.go` cases, the `Modules`
   literal in `internal/module/compose_test.go`.
2. **Migration `00112_google_baseline.sql`** exactly as G12 (connection, credentials,
   events, the one row).
3. **The guarded client** (G14): `client.go` with the fixed hosts, netguard,
   `Deps.HTTPTransport` in tests, budgets, one retry, caps, Google's words never logged;
   `token.go` with `golang.org/x/oauth2/jwt.Config` and the guarded client in the
   context as `oauth2.HTTPClient`; the in-memory token cache; the error mapping.
   `tokenSource(scopes, subject)` is the one seam a later `vantigo_hosted` kind fills.
   Do **not** add `google.golang.org/api`.
4. **Credentials** (G13): `POST /google/connection/credentials` parsing the key file
   (the rules in G13, 8 KiB, sealed under `google/service-account-key`, only the four
   clear fields kept, `client_mismatch` for another account while one is live);
   `DELETE …/{id}`; `PUT /google/connection/oauth-client` (`oauth_client_id` and the
   secret sealed under `google/oauth-client-secret`); `GET`/`PUT /google/connection`
   (`admin_subject`, `credential_kind` refusing `vantigo_hosted` with
   `credential_kind_unsupported`) under `If-Match`. Writes under
   `policy:Owner+OwnerManagement`, reads under `permission:google:manage`; the Owner
   bucket (30 a minute) on every write and the test.
5. **The test** (G15): one token per needed scope (A needs the two customer/domain
   scopes; the table's later rows are added by their deliveries but the mechanism takes
   a list), the fixed unexpected list, `customers.get(my_customer)`, `domains.list`;
   the states `pending_delegation`, `connected`, `failing` with their reasons; events.
   The *scopes to delegate* answer (`GET /google/connection` carries
   `scopesToDelegate`, full URLs, and the service account's numeric client id).
6. **`googletest`**: a fake token endpoint verifying the JWT (issuer = the key's
   `client_email`, `sub`, `scope`, `aud`, RS256 signature against the uploaded key's
   public half) with per-scope grants a test sets; fake `customers.get` and
   `domains.list`.
7. **`GET /google/events`** (`google:manage`).
8. **The frontend**: `apps/google/frontend` as `@vantigo/google-ui` (copy the
   `no-restricted-imports` block, join every deny list, `gen-client`), the Status and
   Connection tabs, the two placeholder tabs; host routes `/workspace/google` with four
   tab routes, the Integrations item (`module: "google"`, `google:manage`), the
   catalogs (en, nb), the permission label, `sessionClients`, `moduleKeys` and
   `ModuleWithApp` excluding `google`. The key is pasted or read from a chosen file in
   the browser and sent as text; after upload the page shows only the service account's
   address, its numeric client id, the key id and the date. The client secret is never
   shown back; the page says Google shows it only once.
9. **Docs** (below), then `go generate ./...` and the regenerated assistant docs.

## The documentation (same commit series)

- New `en/admin/google-workspace.md` and `nb/admin/google-workspace.md`: the procedure
  G16 lists (before you start; the project; the service account and the key, lifting
  `iam.disableServiceAccountKeyCreation` for the project; domain-wide delegation, the
  scopes, up to 24 hours, multi-party approval; the admin to impersonate; the Internal
  web client, its consent screen as *Internal*, its redirect URIs; verify; rotate a
  key; change the service account; what can go wrong). `sources` naming
  `apps/server/internal/google` files it describes.
- New `en/user/google-workspace.md` and `nb/user/google-workspace.md`: where the page
  is, its four tabs, who sees what, the Connection steps as the screen names them.
- New `en/reference/google.md`: the module, the schema, the client and its hosts, the
  token source, the credential rules, the test and its states, the permission and each
  operation's access, the events. `sources: apps/server/internal/google`,
  `apps/google/frontend`, `apps/host/frontend/src/routes/workspace/google`.
- The indexes (`admin/index.md`, `user/index.md` in both languages, `reference/index.md`),
  `admin/configuration.md` (both), `user/workspace-administration.md` (both),
  `contributing/module-boundaries.md` if it lists modules, `ROADMAP.md`.

## Do not

Fill any slot (`IdentityProvisioner` is B's) · add the personal connection (D) · write a
Gmail or Calendar call · show a secret or the key in any form · store anything of the key
file beyond the sealed key and the four clear fields · import another module.

## How to work

- Worktree on `feat/google-foundation` cut from the previous delivery's branch (the README's stack);
  `bun install --frozen-lockfile` first.
- **Plan first**: `docs/superpowers/plans/2026-11-03-google-foundation.md` in the house
  shape, committed alone as `docs(google): the plan for the foundation (Google A)`.
- The migration takes the next free number (expected 00112).
- Commits as the history shows: `feat(google): …`, `feat(google-ui): …`,
  `feat(host): …`, `docs(google): …`, `chore(assistant): regenerate the embedded
  documentation`, each ending `(Google A)` in the subject where the history does so.
  Signed; never bypass signing or hooks.
- **No `Docs-Impact: none` trailer** on any commit: this delivery changes documented
  behaviour, and one trailer waives the check for the whole pull request.
- Gates: `mise run server:check`, `mise run server:test`, `mise run frontend:check`,
  `mise run docs:check`.
- No merge of `main`; the starting session pushes and opens the pull request.

## Report

The branch and `git log --oneline main..`; the gates' output; the plan's path; the
migration number; departures from G12–G16; which of U2 and U14 the test tenant settled;
and the manual check: upload a key, see the client id, test before delegation
(`pending_delegation`), delegate, test again (`connected`, the domains), grant one extra
scope and see it unexpected.
