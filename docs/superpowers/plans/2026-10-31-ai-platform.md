# AI as a platform provider, delivery D — a model per task: implementation plan

> **For agentic workers:** implement this plan task by task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** the AI provider, its key and the models the plan allows are instance
configuration (`AI_PROVIDER`, `AI_API_KEY`, `AI_BASE_URL`, `AI_MODELS`), read once at boot
into one platform package, `srv/ai`, that every module calls through `Deps.AI` with a
**task key** and never with a model or a key. Each module declares the AI tasks it
performs the way it declares permissions. An Owner opens **Workspace admin › Integrations
› AI models** and sees the provider and the allowed models as facts, that a key is
configured (never the key), and one row per task grouped by module with a select of the
allowed models. Communications' two AI operations behave exactly as today on the wire,
now through `Deps.AI` with their task keys. The old `COMMUNICATIONS_AI_*` variables work
for one release as aliases, and `COMMUNICATIONS_AI_MODEL` is imported into the table once.

**Architecture:** `config` reads `AI_*` (and maps the three renamed `COMMUNICATIONS_AI_*`
variables onto them, recording which, for one boot log line). `srv/ai` holds the
provider-neutral `Request`/`Response`, the OpenAI implementation moved out of
`communications/ai_client.go` with its guard and typed errors, and the `Resolver` seam;
it has no SQL and no module import. `contracts` gains `AITask` and `AIModels`;
`module.Module` gains `AITasks` and `AIModels`; `Compose` collects the enabled modules'
tasks into `Deps.AITasks` (duplicates refused), resolves the sole `AIModels` provider
and builds `Deps.AI` from the configuration and that resolver (nil when unconfigured).
Identity fills the slot: `identity.ai_task_models` (migration `00098`), a cached
three-step resolution, the one-time import of `COMMUNICATIONS_AI_MODEL` at the first API
mount that finds the table empty, and two Owner-only endpoints under `owner/ai`.
Communications declares `communications.compose-reply` and
`communications.customer-suggestion` and calls `Deps.AI.Complete`; `ai_client.go` is
deleted. The host gets an `ai` feature gate in the navigation (an Owner's
`GET …/owner/ai/settings` answering), the `/workspace/ai` page and its catalogs.
Branch `feat/ai-platform`, cut from `claude/entra-id-redesign-strategy-7cc8ba` at
`origin/main` (`9706922b`, delivery A merged as #203) plus the brief's correction.

**Spec:** [`docs/superpowers/specs/2026-10-31-integrations-and-configuration-tiers-design.md`](../specs/2026-10-31-integrations-and-configuration-tiers-design.md), T1, T11, T12, T13, T15 (delivery D).
**Brief:** [`…/2026-10-31-integrations-briefs/d-ai-platform.md`](2026-10-31-integrations-briefs/d-ai-platform.md).
**Pattern:** [`2026-10-31-workspace-integrations.md`](2026-10-31-workspace-integrations.md) (delivery A) for the routes, guards and tests.

## Plan readings

1. **The variables today.** `config.go:511-538` holds `CommunicationsAIEnabled`,
   `…Provider` (default `openai`), `…Model` (default `gpt-4o-mini`) and `…APIKey`;
   `communicationsAI` (`:1190`) reads them and nothing refuses to start (unavailable is
   an ordinary state, only the `…ENABLED` flag syntax can fail); `Load` calls it at
   `:645`; `redact.go:45` redacts the key; `redact_test.go:54` and `config_test.go:1130-1190`
   pin them. No alias or deprecation mechanism exists in `config` or `cmd/vantigo`
   (`grep -i "deprecat\|renamed"` finds none that is configuration), so this delivery adds
   one: `Config.Renamed` (`[]RenamedVariable{From, To}`) and one `logger.Warn` in
   `cmd/vantigo/main.go` after the logger exists (`:143`), before the mode switch.
2. **Alias semantics.** Legacy availability = `ENABLED=1` and provider `openai`
   (case-insensitive) and a non-blank key. Mapping onto `AI_*`: `AI_PROVIDER` unset and
   `COMMUNICATIONS_AI_ENABLED=1` → provider is `COMMUNICATIONS_AI_PROVIDER` (default
   `openai`; a value other than `openai` stays *unavailable*, never an error, exactly as
   before); `AI_API_KEY` unset → `COMMUNICATIONS_AI_API_KEY`; `AI_MODELS` unset while the
   provider came from the aliases → the one model `COMMUNICATIONS_AI_MODEL` (default
   `gpt-4o-mini`). `AI_*` always wins; a legacy variable set beside it is named in the
   same log line as ignored. `CommunicationsAIModel` stays on the config, **without** the
   default, for the import only (it must be able to be "unset").
3. **The client being moved.** `ai_client.go` (228 lines): `newAIChatClient` (`:112`) builds
   an `http.Client` with `CheckRedirect: ErrUseLastResponse` over `Deps.HTTPTransport`;
   `complete` (`:154`) bounds 30 s (`aiRequestTimeout`), sends one `user` message and no
   system message, reads the body whole, and returns the four typed errors
   (`aiTransportError` keeping its cause for `errors.Is`, `aiStatusError`, `aiDecodeError`,
   `aiEmptyResponseError`); `tokenCount` (`:218`) narrows to int32. `errorTypeName`
   (`ai.go:181`) renders `%T` after the last dot, so the **error type names are the audit
   labels** (`aiTransportError`…). The moved types keep those exact unexported names, so
   rows written before and after the move carry the same words.
4. **What communications reads of the client.** `server.go:35,72` holds `ai *aiChatClient`;
   `ai.go:287-288` writes `Provider: aiProviderName` and `Model: s.ai.model` into the
   interaction row **before** the call; `:440` and `:639` call `s.ai.complete(ctx, prompt)`;
   `:403` and `:571` answer 503 `ai_unavailable` (`aiUnavailableMessage`, `:104`) when
   `s.ai == nil`; `callerGaveUp(ctx, err)` asks only the request context (`:218`). With
   the model chosen per task and resolved at call time, the row's `provider` and `model`
   must come from the answer: `ai.Response` carries `Provider` and `Model` and is returned
   (those two set, the rest zero) **with** an error whenever the model was resolved, so the
   exception path still names the model it asked. `ai_interactions.model` is
   `varchar(150) NOT NULL`, so `limitUTF16(model, 150)` stays where the row is built.
5. **Tests that name the old wiring.** `ai_test.go:111-119` (`newAIHarness`) sets
   `COMMUNICATIONS_AI_ENABLED/_API_KEY/_MODEL`; it becomes `AI_PROVIDER`, `AI_API_KEY`,
   `AI_MODELS` — the only construction change. `ai_test.go:238-239,259-260` assert the exact
   503 message `Communications AI is not available.`; the brief gives that sentence a
   second one, so those two assertions change with it (the one deliberate non-construction
   edit, recorded as a departure). Everything else in the 37 tests keeps driving the real
   client through `fakeChatTransport` (the blind-seam argument moves to `srv/ai`'s header).
6. **How Compose collects.** `compose.go:150-165` builds `catalog` from each enabled
   module's `Permissions` through `contracts.ValidatePermission` and refuses a duplicate;
   `AITasks` joins that loop (`contracts.ValidateAITask`, duplicate key refused, a disabled
   module's absent because `enabledModules` already dropped it). The single-provider slots
   resolve through `soleProvider` (`compose.go:480`) and a `with…` helper shared with
   `Workers` (`workers.go:196`); `AIModels` follows `CalendarBusy` (`compose.go:390`, the
   slot Workers does not resolve): **Workers does not resolve AI**, since no worker
   performs a task and a task is declared by the delivery that performs it. `modDeps.Catalog`
   is set per module at `:410`; `Deps.AITasks` is set on the shared `deps` before the slots
   so identity's provider can read it while it is built.
7. **The harness.** `modtest.New` composes identity plus the module under test over `WithEnv`
   (`modtest.go:537`) and builds the client through `Compose` — so Communications' tests
   reach the real identity resolver with an empty table (plan default) and the new tests
   can write rows with `h.Exec`. `Deps.HTTPTransport` is the seam (`WithTransport`).
8. **Identity patterns reused.** `sso_settings.go:163` (`ssoSettingsReader`: TTL cache,
   never holds its mutex across the read, `evict`), `:299-370` (the import: one
   transaction, a no-actor audit event, names not values, a warning), `sso_settings_owner.go:70-140,288`
   (`If-Match` parsed, `412 version_mismatch`, `changeSSOSettings`, `refuse`/`refusalOr`),
   `module.go:58` (`limits` map: the owner SSO operations use `policyUserManagement`),
   `audit.go` (`writeAudit`, `auditEvent{actor, action, before, after}`). Identity's contract
   is `openapi/identity.yaml` (`x-vantigo-access: policy:Owner`, the SSO paths at
   `/api/v1/identity/owner/sso`); generated Go in `internal/identity/gen`, TypeScript in
   `apps/host/frontend/src/api-schema.d.ts` by `bun run gen:client`; sqlc schema list in
   `internal/identity/sqlc.yaml` must name the new migration (`schema_test` keeps it in step).
9. **Endpoint paths.** The brief writes `/api/v1/ai/…`; Compose mounts a module at
   `/api/v1/<name>/` and identity's Owner-only endpoints live under `/api/v1/identity/owner/`.
   The endpoints are therefore `GET /api/v1/identity/owner/ai/settings` and
   `PUT /api/v1/identity/owner/ai/tasks/{key}`. Recorded as a departure.
10. **`If-Match` on a table with no version column.** SSO's `If-Match` is the row's integer
    `version`. The table the spec fixes is `(task_key, model, updated_at, updated_by)`;
    a task with no row has no state. Each task is answered with `version`: the
    microseconds of its row's `updated_at` since the epoch, `"0"` when there is no row;
    `PUT` takes the row `FOR UPDATE` (or the absence of it) and compares. Another Owner's
    save in between → `412 version_mismatch`. No column is added.
11. **How the nav can be gated on an instance fact.** `NavItem.module` is judged against
    `enabledModules`, a synchronous list injected into the page. The AI provider is
    instance configuration too but not injected, and `NavVisibilityContext` has no slot
    for it. The brief says "present while `GET …/ai/settings` answers", so the root layout
    (`routes/__root.tsx:100-140`, where the authorization query already lives) asks for it
    when the person is an Owner (`useQuery`, `retry: false`, `staleTime` 5 min) and passes
    `features: ["ai"]` into the visibility context when it answered; `NavItem.feature?:
    "ai"` hides the item otherwise, in the sidebar, the spotlight and the module-access
    guard alike (all three read `visibleNavSections`/`allNavSections`). The route's
    `beforeLoad` fetches the same query and throws not-found when it does not answer.
12. **Docs the change touches.** `sources` already naming `apps/server/internal/config`:
    `admin/configuration.md` (en, nb); identity's code is claimed by
    `user/workspace-administration.md`, `admin/authentication.md`, `admin/sso-scim.md`; the
    communications code by `reference/communications.md` and `user/communications.md`;
    `contributing/platform-packages.md` and `module-boundaries.md` are English only.
    `deploy/compose/vantigo.env.example` and `deploy/compose/README.md` name
    `COMMUNICATIONS_AI_*` and gain `AI_*`. Identity has no reference page: its endpoints are
    reached through the generated API reference (`reference/api/`), so the contract is the
    documentation and the *AI models* section lands in `admin/configuration.md` (the table
    and its import) and `user/workspace-administration.md` (the page); the identity `sources`
    of the latter list the files this delivery touches.

## Tasks

- [ ] **1. `feat(config)`: `AI_*` and the renamed variables.** `AIProvider`, `AIAPIKey`,
  `AIBaseURL`, `AIModels` on `Config` (validation: provider `openai` or empty; key required
  with the provider and printable without space; base URL an absolute `https` URL with no
  user/query/fragment; models a comma list of 1–150-character printable tokens, at least
  one with the provider, no duplicate; `AI_API_KEY`/`AI_MODELS`/`AI_BASE_URL` without the
  provider refused); the aliases of plan reading 2; `CommunicationsAIModel` without a
  default; `Renamed`; the key redacted. `communications/ai_client.go` reads the new fields
  for now (one model: `AIModels[0]`) so the commit builds and every test passes. `main.go`
  logs the renamed variables once. Tests: defaults, configured, each refusal, each alias
  case, `AI_*` winning over a legacy value, the redaction.
- [ ] **2. `feat(ai)`: the platform package.** `srv/ai`: `Request{Prompt}`,
  `Response{Text, InputTokens, OutputTokens, Provider, Model}`, `Resolver`, `New(cfg,
  transport, resolver) *Client` (nil unless `AIProvider` is set), `Client.Complete(ctx,
  task, req)` — resolve the task's model, refuse one outside `AI_MODELS` (`modelNotAllowedError`,
  nothing sent), POST `{base}/chat/completions` with one `user` message under a 30 s
  budget, no redirects, `Deps.HTTPTransport`-style transport, the four typed errors with
  their names, token narrowing — plus `Client.Provider()`, `Client.Models()`. The blind-seam
  header moves with the code. Tests (package `ai_test`, a fake `RoundTripper`): the request
  on the wire (URL from `AI_BASE_URL`, bearer, body), each typed error and its type name,
  the cancellation kept for `errors.Is`, a disallowed model refused with no request, a nil
  resolver using the plan default, an unconfigured config giving nil.
- [ ] **3. `feat(module)`: tasks, the `AIModels` slot and `Deps.AI`.** `contracts/ai.go`
  (`AITask`, `ValidateAITask`, `AIModels`); `module.Deps.AITasks`, `Deps.AI`;
  `Module.AITasks`, `Module.AIModels`; Compose collects and validates tasks in the catalog
  loop, resolves the sole `AIModels` provider (two refused, naming both), builds `Deps.AI`
  with it unless a harness preset one. `modtest` gains `WithAIModels` (a fake resolver) so
  module tests can pin the choice. Tests (`module/compose_test.go`, `contracts/ai_test.go`):
  a task is collected, a disabled module's is absent, a duplicate key and a wrong module
  prefix are refused, two `AIModels` providers are refused, `Deps.AI` is nil without
  `AI_PROVIDER` and non-nil with it, and calls the provider's resolver.
- [ ] **4. `feat(communications)`: the two operations through `Deps.AI`.** Declare
  `communications.compose-reply` ("Draft a reply") and `communications.customer-suggestion`
  ("Suggest the customer"); `server.ai` becomes `deps.AI`; `s.deps.AI.Complete(ctx,
  task, ai.Request{Prompt})`; the row's `provider` and `model` from the answer; delete
  `ai_client.go` and the dead constants; the 503 sentence gains "An Owner chooses models
  under Workspace admin › AI models."; `newAIHarness` sets `AI_*`; the two message
  assertions follow. Tests: the 37 existing ones green; new — each operation's request
  names the model `WithAIModels` chose for **its** task, the row records it, and a
  disallowed answer from the resolver is audited as an exception row.
- [ ] **5. `feat(identity)`: the model per task.** Migration `00098_identity_ai_task_models.sql`
  (`task_key text PK`, `model varchar(150) NOT NULL`, `updated_at timestamptz NOT NULL`,
  `updated_by uuid NULL`; Down drops it); `sqlc.yaml` schema list; `queries/ai_task_models.sql`
  (list, get-for-update, upsert, count) and `store` regenerated; `ai_models.go`: the
  resolver (20 s cache, evicted by this process's save), `resolveTaskModel(row, task,
  allowed) (model, source)` shared with the endpoint, `Module.AIModels`; the import in
  `ai_models_import.go` at identity's mount, beside `importSSOSettingsFromEnvironment`: in one
  transaction under `LOCK TABLE … SHARE ROW EXCLUSIVE`, an empty table with
  `COMMUNICATIONS_AI_MODEL` set, AI configured and the model allowed → one row per enabled
  `communications.*` task, audited `ai-task-models.imported` (names, no actor) and one
  warning "imported from the environment; the variable is now ignored and should be
  removed"; a table already written with the variable still set → a warning that it is
  ignored; set but not allowed, or AI unconfigured → a warning that it was not imported.
  Tests (`ai_models_test.go`, `ai_models_import_test.go`, white-box where the cache needs
  the clock): the three steps (row allowed / row no longer allowed → default / default not
  allowed → plan), the cache and its eviction, the import once and never twice (second mount,
  rows surviving, a changed variable ignored), the not-allowed and unconfigured cases, only
  `communications.*` tasks imported.
- [ ] **6. `feat(identity)`: the AI settings endpoints.** `openapi/identity.yaml`:
  `getIdentityOwnerAiSettings`, `putIdentityOwnerAiTasksByKey` (`policy:Owner`, rate limit
  `policyUserManagement`, the schemas `AiSettings`, `AiModule`, `AiTask`, `AiTaskRequest`);
  `go generate` for the Go contract, the embedded copy and `bun run gen:client` for
  `api-schema.d.ts`; `ai_settings.go`: GET (404 `ai_not_configured` while there is no
  provider; provider, `allowedModels`, `planDefault`, `keyConfigured: true`, modules in key
  order with tasks `key, display, description, model, default, source, version`), PUT
  (`If-Match` the task's `version`, 400 on a malformed one, 404 unknown task, 422
  `model_not_allowed`, 412 `version_mismatch`, one transaction, upsert, `updated_by`,
  audit `ai-task-model.changed` with the model before and after, the resolver evicted,
  200 the task). Tests (`ai_settings_test.go`): Owner-only (a role holding every permission
  but not Owner → 403; anonymous → 401), the response shape and the key's absence from the
  body and the audit, `source` for chosen/default/plan, 412 on a stale version and success on
  the fresh one, 422, 404 for an unknown task and for no provider, the audit row, the change
  taking effect on the very next `ModelFor`, a module that is off not listed.
- [ ] **7. `feat(host)`: the *AI models* page.** `api/ai.ts` (`fetchAiSettings`,
  `updateAiTask`, query key); `NavItem.feature` and `NavVisibilityContext.features`
  (`navigation.ts`), the spotlight prop; the root layout's Owner-only query; `apps.ts`:
  the Integrations section's second item (`navigation.aiModels`, `to: "/workspace/ai"`,
  `ownerOnly`, `feature: "ai"`); `routes/workspace/ai.tsx` (gate: Owner and the query
  answering, else not-found) with `-ai-models.tsx` (facts block, tasks grouped by module,
  a select per task, *Default*/*Plan default* badge, every select locked when one model is
  allowed, save under `If-Match` with the version-mismatch notification the SSO card uses,
  reload on a 412); catalogs en and nb (`catalogs/navigation.ts`, `catalogs/admin.ts`);
  `routeTree.gen.ts`. Tests: `apps.test.ts` (the item hidden without the feature, shown to
  an Owner with it, hidden from a non-Owner), `route-guards.test.ts` (the route's gate),
  `routes/workspace/ai.test.tsx` (facts, grouping, saving, locked state with one model,
  conflict), `catalogs/parity.test.ts` unchanged and green.
- [ ] **8. `docs(admin)`: configuration, authentication, installation — en and nb.**
  `admin/configuration.md`: `AI_*` in the instance table, the renamed variables and their
  release, the import of `COMMUNICATIONS_AI_MODEL`, *AI models* in the workspace table;
  `admin/authentication.md`: the AI settings leave the Communications paragraph and point to
  Configuration; `admin/installation.md`: the AI variables; `deploy/compose/vantigo.env.example`
  and `README.md`.
- [ ] **9. `docs(user)`: *AI models* in Workspace administration — en and nb.**
  `user/workspace-administration.md`: what the Owner sees (provider, allowed models, "an API
  key is configured by Vantigo"), what the Owner changes (a model per task), what Vantigo
  decides; `user/communications.md`: the AI draft's availability sentence points to it.
- [ ] **10. `docs(reference)`: the task keys, `Deps.AI`, the slot, the roadmap.**
  `reference/communications.md` (*AI draft and customer suggestion*: the task keys, `Deps.AI`,
  the new sentence, the aliases); `contributing/platform-packages.md` (`ai`);
  `contributing/module-boundaries.md` (the `AIModels` slot, `AITasks`); `ROADMAP.md`.
- [ ] **11. The gates.** `mise run server:check`, `mise run server:test`,
  `mise run frontend:check`, `mise run docs:check`; `go generate ./...` and `bun run
  gen:client` leave no diff; fix what they find in the task that caused it.
