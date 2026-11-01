# The Assistant, delivery A — the Assistant app (read): implementation plan

> **For agentic workers:** implement this plan task by task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** a person with the assistant available sees an **Assistant** button in the shell
header and opens a docked panel (Cmd/Ctrl+J, the button, or Spotlight's *Ask the
assistant*), asks "how do I…" and gets steps from the documentation, asks for data and
gets a table read through the operations they may call, and is told plainly that changes
come later. The **Assistant app** at `/assistant` lists every conversation with search,
rename, pin, archive, delete and resume. **Workspace admin › Assistant** (Owner) holds the
mode, the retention, the data notice and *What the assistant may use* (S16); the user page
holds the per-person switch.

**Architecture:** a new module `assistant` (schema `assistant`, mount `/api/v1/assistant/`,
AI task `assistant.chat`, no permissions of its own). It runs a model loop in the request
but detached from the client: `ai.Chat` streams, the five tools (`search_docs`,
`search_operations`, `describe_operation`, `call_operation`, `navigate`) answer through the
embedded documentation index and `Deps.Operations`, every message and action is persisted as
it happens, and the stream is `text/event-stream` flushed per event. The dispatcher
(`internal/operations`) splits the contract's mode from a **gate** the module installs
(`SetGate`) that applies the Owner's permission levels (S16); without a gate it keeps
delivery 0's fail-closed rule. The frontend is `apps/assistant/frontend`
(`@vantigo/assistant-ui`): the app pages, the panel, the streaming client and three host
slots; the host mounts the panel in the app layout. Branch `feat/assistant-app`, cut from
`origin/main` `5c70eb45` after delivery 0 (#213).

**Spec:** [`docs/superpowers/specs/2026-11-01-assistant-design.md`](../specs/2026-11-01-assistant-design.md), S1, S5 (A's tools), S8, S9, S10, S14 (per-run caps), S16.
**Brief:** [`…/2026-11-01-assistant-briefs/a-assistant-app.md`](2026-11-01-assistant-briefs/a-assistant-app.md).
**Predecessor:** [`2026-11-01-assistant-platform.md`](2026-11-01-assistant-platform.md) (delivery 0).

## Plan readings

1. **The dispatcher.** `operations/mode.go` `effectiveMode` folds S4's `Sensitive` clause into
   the one mode; `Info` has no permissions; `List`, `Search` and `Call` read `e.Mode`
   (`operations.go`). `Bind` computes modes once. `TestOperations_TheRealContract`
   (`integration/assistant_test.go:76`) derives each mode again from the contract and the
   catalog. `modtest.Harness.Operations` builds a dispatcher with no gate.
2. **Module shape.** `planning` is the template: `Module()` → `module.Module{Name, Permissions,
   Mount}`; `mount` builds `module.NewRouter` with `d.Doc`, `d.Access`, `d.Limiter`, `d.Catalog`,
   wraps `gen.NewStrictHandlerWithOptions`; the generated strict interface is satisfied by
   `server` methods; Owner settings use a revision under `FOR UPDATE` and `If-Match`
   (`planning/settings.go`). A strict response object may implement its own `Visit…Response`,
   which is how the SSE operation writes its own body.
3. **Registration.** `Modules` in `openapi/openapi.go:25`, `moduleSchemas` in
   `db/schema_test.go:28`, `businessModules` in `cmd/vantigo/main.go`, the `Modules` literal in
   `module/compose_test.go:617`, `generate.go` (oapi-codegen + sqlc lines),
   `openapi/gen/cfg-<name>.yaml`, depguard in `apps/server/.golangci.yml`, the config's known
   and default module lists, `docs/astro.config.mjs` (`contracts`), `tools/openapi/gen-client.ts`,
   the host's `navigation.ts`, `apps.ts`, `i18n.ts`, `catalogs/`, and the deploy and README
   module lists.
4. **Compose and Deps.** `Compose` gives `Deps.Operations` only to the module named
   `assistant` (`module/compose.go:443-463`) and binds it once the router exists (`:533`), so it is
   for use while serving. `Deps.AI` is nil when no provider is configured; `Deps.AssistantGrants`
   is identity's; `Issue` needs the person's own request on ctx (`contracts.WithRequest`, which
   the router already puts on a handler's ctx) and a ttl of at most one hour.
5. **Local provider.** `config.localAIHost` is unexported; the module needs to tell the panel
   whether the provider's URL is local, so `Config` gains `AILocal()` over it. `AI_CONTEXT_TOKENS`
   is a new integer (default 16000).
6. **Erasure.** Identity deletes a user with `DELETE FROM identity.users`
   (`DeleteIdentityOwnerUsersById`, `AccountProvisioning.Erase`); its own tables cascade, but no
   module is told, and no module may reference another's schema. The existing sanctioned read
   is `contracts.UserDirectory.Users(ids)` ("an id that does not exist is simply absent"), which
   Workers also sets. So the seam used is a sweep in the assistant's own daily worker: it asks
   `Users` about the user ids it holds, in chunks, and deletes the rows of the absent ones.
   No new contract is needed. Grants cascade in identity already.
7. **Audit.** Identity's `writeAudit` writes `identity.audit_events` and is identity's alone
   (a module may not write another schema, and there is no audit slot). The Owner's changes are
   audited in `assistant.events` with `before` and `after`, written in the same transaction as
   the change.
8. **Docs.** Pages are in `docs/src/content/docs/{en,nb}/{user,admin}` and `en/reference`; each
   declares `sources`. `bun run tools/docs/check-coverage.ts --list` prints the map. The docs
   site is built by Astro Starlight, so anchors follow its slugger (github-slugger).
9. **Frontend.** `apps/entra/frontend` and `apps/calendar/frontend` show the package shape; the
   host mounts a module in `apps.ts`, `navigation.ts`, `routes/<mod>.tsx`, `i18n.ts`. There is no
   Markdown renderer in the frontend lockfile; one is added (react-markdown, which renders no raw
   HTML, with remark-gfm for tables).

## Tasks

- [ ] **1. The plan.** This file, committed alone, with no `Docs-Impact` trailer.
- [ ] **2. Config.** `AI_CONTEXT_TOKENS` (`Config.AIContextTokens`, default 16000), `Config.AILocal()`,
  `assistant` in the known and default module lists. Tests.
- [ ] **3. The gate (S16).** `internal/operations`: the contract mode without the `Sensitive`
  clause, `Info.Permissions` and `Info.Sensitive`, `SetGate`, the default fail-closed gate,
  `List`, `Search` and `Call` applying it before the confirmation check. Update the dispatcher's
  tests and `TestOperations_TheRealContract` (contract modes and default-gate modes pinned
  separately).
- [ ] **4. The module's schema and contract.** Migration `00103_assistant_baseline.sql`
  (`settings`, `person_settings`, `conversations`, `messages`, `runs`, `actions`,
  `permission_levels`, `events`), sqlc queries, `openapi/assistant.yaml` (every operation with a
  summary, `x-vantigo-access` and `x-vantigo-assistant: never`), codegen configs, registration
  everywhere in reading 3, `Module()`, depguard, schema test, the generated TypeScript client.
- [ ] **5. Availability, settings, people, me (S8).** `available()`, the availability endpoint, the
  Owner endpoints under `If-Match` with audit in `assistant.events`, `GET`/`PUT …/me`. Tests over
  all mode and switch combinations and the Owner endpoints.
- [ ] **6. What the assistant may use (S16).** The `permission_levels` endpoints, the gate the
  module installs at mount, cached by the settings' version. Tests for every level, the switch,
  overrides surviving a switch change, narrowed operations absent from search and refused by
  `Call`, an uncallable operation staying uncallable.
- [ ] **7. Conversations (S9).** List, create, get, patch, delete; per-user queries; the privacy
  isolation test (404 not 403 on every endpoint).
- [ ] **8. The documentation index.** `go:generate` copy of the user and admin pages into the
  module, embedded; the BM25 index, section splitting, Starlight anchors; the freshness test.
- [ ] **9. Runs, tools and the stream (S5, S14).** The run loop, the five tools, the system prompt,
  compaction, the caps, grants per run, detached context, `409 run_in_progress`, stop, the SSE
  response. Tests through a fake `ai` transport scripted with tool calls.
- [ ] **10. Workers and erasure (S9).** Retention (daily, chunked, advisory lock), stale runs,
  the erasure sweep. Tests.
- [ ] **11. The frontend package (S10).** `apps/assistant/frontend`: pages, panel, streaming
  client, `assistantContext`, the three slots, i18n (en, nb). Tests.
- [ ] **12. The host.** `apps.ts` entry, routes, the header button and badge, Cmd/Ctrl+J, the
  panel in the app layout, Spotlight, Workspace admin › Assistant, the user page switch, the
  `assistantContext` wiring for customers, energy metering points, invoices and projects. Tests.
- [ ] **13. Documentation.** In one commit, so the coverage check sees the pages together:
  - `user/assistant.md` (en, nb), listed in `user/index.md`;
  - `user/workspace-administration.md` (en, nb): Assistant, the levels, the per-person switch;
  - `admin/configuration.md` (en, nb): `AI_CONTEXT_TOKENS`, tool calling;
  - `reference/assistant.md` (en), listed in `reference/index.md`;
  - `contributing/module-boundaries.md`: the module's slots, `Operations` and `SetGate`, the
    erasure rule;
  - `ROADMAP.md`: A done.

  No commit in the range carries a `Docs-Impact` trailer: a `none` anywhere would waive the
  coverage check for the whole pull request.
