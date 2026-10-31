# Brief D — AI as a platform provider, a model per task

You are implementing delivery **D** of the design
`docs/superpowers/specs/2026-10-31-integrations-and-configuration-tiers-design.md`
(decisions **T11, T12, T13, T15**). Read that file first, in full, then this brief.
Delivery **A** should already be on `main` (grep `navigation.integrations` in
`apps/host/frontend/src/catalogs/navigation.ts`); if it is not, build the page and the
item anyway and say so. You work alone, from a cold start, in this repository. Model:
Claude Sonnet 5.5.

## The outcome

The AI provider, its key and the models the plan allows are instance configuration
(`AI_PROVIDER`, `AI_API_KEY`, `AI_BASE_URL`, `AI_MODELS`), read once at boot into one
platform package, `srv/ai`, that every module calls through `Deps.AI` with a **task
key** and never with a model or a key. Each module declares the AI tasks it performs
the way it declares permissions. An Owner opens **Workspace admin › Integrations › AI
models** and sees the provider and the allowed models as facts, that a key is
configured (never the key), and one row per task grouped by module with a select of the
allowed models. Communications' two AI operations behave exactly as today on the wire,
now through `Deps.AI` with their task keys. The old `COMMUNICATIONS_AI_*` variables
work for one release as aliases, and `COMMUNICATIONS_AI_MODEL` is imported into the
table once.

## Read before you plan

- `AGENTS.md`, `CONTRIBUTING.md`, `docs/src/content/docs/en/contributing/module-boundaries.md`
  (slots, how Compose composes `Deps.Catalog`), `en/contributing/platform-packages.md`.
- The spec's *Readings*, T1, T11–T13, T15; the Entra design D1 for the one-time import
  from the environment (`docs/superpowers/specs/2026-10-19-entra-design.md`) and its
  code, `apps/server/internal/identity/sso_settings.go`.
- `apps/server/internal/config/config.go` (`communicationsAI`, the
  `COMMUNICATIONS_AI_*` fields, how aliases and deprecations are logged elsewhere),
  `config/redact.go`.
- `apps/server/internal/communications/ai_client.go` (the client you move: its guard,
  typed errors, token counting, the blind-seam argument in its header comment — keep
  it true), `ai.go` (the two operations, their asymmetries and `ai_interactions`
  rows — do not change their behaviour), `ai_test.go` (`fakeChatTransport`).
- `apps/server/internal/module/module.go` (`Module.Permissions`, `Deps.Catalog`, the
  single-provider slot pattern), `apps/server/internal/server/` (Compose),
  `apps/server/internal/contracts/permission.go` and `users.go` (identity's slot as a
  model), `apps/server/internal/modtest/`.
- Identity: `module.go` (its API mount and how Owner-only endpoints are declared),
  `openapi/identity.yaml` (or wherever identity's contract lives — check
  `openapi/`), `system_settings` and `sso_settings` queries, `audit.go`.
- Host: `apps/host/frontend/src/apps.ts` (the Integrations section after A),
  `routes/workspace/`, `routes/workspace/-sso-settings.tsx` (an Owner card with
  `If-Match`), `catalogs/`, the `parity.test.ts`.
- Docs: `en/admin/configuration.md` (from A), `en/admin/authentication.md` (the AI
  settings paragraph), `en/user/workspace-administration.md`, `en/reference/communications.md`
  (*AI draft and customer suggestion*), `en/contributing/platform-packages.md`,
  `module-boundaries.md`, and the `nb/` twins.

## Build

1. **Config**: `AI_PROVIDER` (`openai`; empty = unavailable), `AI_API_KEY` (required
   with the provider, redacted), `AI_BASE_URL` (optional absolute https URL),
   `AI_MODELS` (comma list, ≥ 1 when the provider is set; first is the plan default).
   `COMMUNICATIONS_AI_ENABLED`, `_PROVIDER`, `_API_KEY` map onto `AI_*` when `AI_*` is
   unset, with one boot log line naming them renamed; `COMMUNICATIONS_AI_MODEL` is kept
   on the config for the import only.
2. **`srv/ai`**: `Request`/`Response` types provider-neutral; `Client.Complete(ctx, task,
   req)`; the OpenAI implementation moved from `ai_client.go` with its guard (no
   redirects, `Deps.HTTPTransport`, budget) and typed errors (transport, status,
   decode, empty) so consumers' `error_summary` labels stay the same; a `Resolver`
   interface `ModelFor(ctx, task) (string, error)`; refusal of a model outside
   `AI_MODELS`. No SQL, no module import. Nil client = unavailable.
3. **Tasks**: `contracts.AITask{Key, Module, Display, Description, Default}`;
   `module.Module.AITasks`; Compose collects into `Deps.AITasks` (a map by key,
   disabled modules absent, duplicate keys refused). Communications declares
   `communications.compose-reply` and `communications.customer-suggestion` (nothing for
   inbound yet — a task is declared by the delivery that performs it).
4. **Identity**: migration `identity.ai_task_models(task_key text PK, model text NOT
   NULL, updated_at timestamptz NOT NULL, updated_by uuid NULL)`; the slot
   `contracts.AIModels` filled by identity (`module.Module.AIModels`), its `ModelFor`
   reading through a short cache (the `sso_settings` pattern) with the three-step
   resolution — the row if allowed, else the task's `Default` if allowed, else
   `AI_MODELS[0]`; the import at the first API mount that finds the table empty while
   `COMMUNICATIONS_AI_MODEL` is set (one row per `communications.*` task; one log line
   "imported from the environment; the variable is now ignored"); endpoints
   `GET /api/v1/ai/settings` (provider, `allowedModels`, `keyConfigured: true`, tasks
   grouped by module with `model`, `default`, `source: chosen|default|plan`) and
   `PUT /api/v1/ai/tasks/{key}` `{model}` under `If-Match`, Owner-only, 422
   `model_not_allowed`, 404 unknown task; audit `ai-task-model.changed` with before and
   after. Compose wires `ai.Client` with identity's resolver into `Deps.AI`.
5. **Communications** calls `Deps.AI.Complete` with its task keys; `ai_client.go`'s
   module-specific remains are deleted; the 503 `ai_unavailable` sentence gains "an
   Owner chooses models under Workspace admin › AI models". The interaction rows,
   permissions, 404 shapes and the fall-through documented in `ai.go` are unchanged —
   prove it with the existing tests passing unmodified except for construction.
6. **The page** `/workspace/ai` under Integrations, `ownerOnly`, present while
   `GET /ai/settings` answers (the item hides and the route answers not-found when AI is
   unconfigured): the facts block (provider, allowed models, "An API key is configured
   by Vantigo"), the tasks grouped by module, a select per task with a *Default* /
   *Plan default* badge, every select locked when one model is allowed; saves under
   `If-Match` with the version-mismatch notification pattern the SSO card uses.
7. **Documentation**, en and nb where bilingual: `admin/configuration.md` (`AI_*`, the
   renamed variables, the import), `admin/authentication.md` (the AI paragraph moves or
   points), `user/workspace-administration.md` (*AI models*: what the Owner sees and
   can change, what Vantigo decides), `reference/communications.md` (*AI draft and
   customer suggestion*: the task keys, `Deps.AI`, the new sentence), a reference
   section for the identity endpoints and the table (where identity's reference lives
   — check `reference/index.md`), `platform-packages.md` (`ai`), `module-boundaries.md`
   (`AIModels` slot, `AITasks`), `ROADMAP.md`.
8. **Tests**: config aliases and the renamed-variables log; `ai` client through a fake
   transport (moved tests), a disallowed model refused; the resolver's three steps; the
   import once and never twice; the endpoints' permission, `If-Match`, 422 and 404;
   Communications' AI tests green with only construction changed; the page hidden
   without a provider, locked with one model; `catalogs/parity.test.ts`.

## Do not

Add a second provider (Q4; keep the types neutral) · a fallback model per task · an
Owner-entered key · show the key in any form · change Communications' AI behaviour on
the wire · declare tasks for features that do not exist yet · touch Entra's tabs.

## How to work

- Worktree on `feat/ai-platform` cut from `main` after A landed;
  `bun install --frozen-lockfile` first; `mise run server:db` for the test database.
- **Plan first**: `docs/superpowers/plans/2026-10-31-ai-platform.md` in the house shape
  (`> For agentic workers`, **Goal**, **Architecture**, **Spec**, *Plan readings* with
  verified line numbers, *Tasks* as checkboxes, one commit each). Commit it alone with
  no `Docs-Impact` trailer (a `none` trailer anywhere in the range waives the coverage check for the whole pull request; plan files need none).
- One migration, `000NN_identity_ai_task_models.sql`, the next free number when its
  task lands; keep the branch rebaseable on `main`.
- One commit per task, Conventional Commits, SSH-signed through 1Password; if signing
  fails, stop and report — never `--no-gpg-sign` or `--no-verify`.
- Gates: `mise run server:check`, `mise run server:test`, `mise run frontend:check`,
  `mise run docs:check`. The Node 26 quirk applies to `apps/projects/frontend` only.
- No push, no pull request, no merging `main` into the branch.

## Report

Branch and `git log --oneline main..`; gates verbatim; the plan's path; the migration
number; departures from the spec; the manual check: set `AI_PROVIDER`, `AI_API_KEY`
and two models, open AI models as an Owner, change a task's model, draft a reply in
Communications and see the interaction row name the chosen model's outcome.
