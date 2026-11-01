# Brief 0: the Assistant's platform

You are implementing delivery **0** of the design
`docs/superpowers/specs/2026-11-01-assistant-design.md`. It carries decisions **S2, S3,
S4, S6, S7 and S15**.

Read that file in full first, then this brief. You work alone, from a cold start, in
this repository. Model: Claude Sonnet 5.5.

## The outcome

There is **no user-visible screen** in this delivery. It builds the floor that delivery
A's assistant stands on, and proves each part with tests:

1. **`ai.Chat`.** It takes a system message, a list of typed messages and JSON Schema
   tools, and streams text deltas. It answers the assistant message with its tool calls
   and the token counts. It does this against any OpenAI-compatible endpoint: OpenAI,
   Ollama, vLLM, LM Studio.
2. **Local models are configurable.** `AI_PROVIDER=openai-compatible` is accepted, and
   `AI_API_KEY` may be empty when `AI_BASE_URL` is set. `AI_BASE_URL` may be `http://`
   for a loopback, private or single-label host. `AI_TIMEOUT` exists.
3. **Identity issues assistant grants** through a new single-provider contract slot.
   `Access.Check` accepts a request whose **context** carries a current grant, and
   answers the session's own principal with `Via` set.
4. **A platform `Operations` dispatcher.** Compose builds it from the merged contract
   and the catalog. It lists the operations a user may call with each one's assistant
   mode, describes one in compact form, and calls one in-process through the composed
   router, as the grant's principal. `Compose` hands it only to a module named
   `assistant`.
5. **The contract and its lint:**
   - `x-vantigo-assistant` is validated by the OpenAPI lint;
   - every operation has a `summary`, and the lint now requires one;
   - read-only `POST`s (searches, lookups) are marked `x-vantigo-assistant: auto`.
6. **The documentation:**
   - the new module-boundary rule;
   - `platform-packages.md` (ai's `Chat`, `operations`);
   - `admin/configuration.md` in en and nb (the `AI_*` changes, with an Ollama
     example);
   - `admin/authentication.md` in en and nb (assistant grants: what they are, how they
     end).

## Read before you plan

- `AGENTS.md`, `CONTRIBUTING.md`.
- `docs/src/content/docs/en/contributing/module-boundaries.md`, in full: rule 6, the
  slot pattern, "Adding a module".
- `en/contributing/platform-packages.md`.
- The spec's *Readings*, S2–S7, S15.
- `apps/server/internal/ai/` (all of it: the guard, the typed errors, the "real client,
  fake transport" argument in the package comment, which stays true).
- `apps/server/internal/config/config.go` (the `AI_*` block near lines 512 and 1290)
  and `config/redact.go`.
- `apps/server/internal/contracts/access.go` (`Principal`, `AsUser`, `HasPermissions`)
  and `contracts/permission.go` (the rule grammar, `Permission.Sensitive`).
- `apps/server/internal/module/router.go` (`wrap`) and `module/compose.go` (the merged
  contract at `/api/openapi.json`, how slots are resolved, how `Deps` is built per
  module) and `module/module.go`.
- `apps/server/internal/identity/access.go` (`Check`, `sessionTokenFrom`, `UserHolds`,
  `businessSatisfied`), identity's session queries and the session table in
  `db/migrations/00002_identity_baseline.sql` and later.
- `apps/server/internal/ratelimit/` (what a policy is keyed by).
- `apps/server/internal/netguard/` (classifiers for loopback and private hosts).
- `apps/server/internal/openapi/lint.go` and its tests.
- `apps/server/generate.go`.
- `apps/server/.golangci.yml` (depguard).
- `openapi/common.yaml` and two module contracts for the shape.

## Build

1. **`ai.Chat`** (S6), in `internal/ai`:
   - **Types:** `ChatRequest{System string; Messages []Message; Tools []Tool;
     MaxTokens int}`. `Message{Role; Text; ToolCalls []ToolCall; ToolCallID}`, where
     the roles are `user`, `assistant` and `tool`. `Tool{Name, Description, Parameters
     json.RawMessage}`. `ToolCall{ID, Name, Arguments json.RawMessage}`.
     `ChatResponse{Message; FinishReason; InputTokens, OutputTokens *int32; Provider,
     Model}`.
   - **Signature:** `Client.Chat(ctx, task string, req ChatRequest, onDelta
     func(text string)) (ChatResponse, error)`, with the same resolve, allow and guard
     sequence as `Complete`.
   - **The OpenAI-compatible implementation:**
     - streams Chat Completions with `stream: true` and `stream_options.include_usage`;
     - parses `data:` lines up to `[DONE]`;
     - accumulates `delta.tool_calls` by index;
     - tolerates providers that send no usage (Ollama) and providers that send the
       whole tool call in one chunk.
   - **Typed errors as `Complete`'s:** transport, status, decode, empty. A stream that
     ends without `[DONE]` is a decode error.
   - **Tests:** canned SSE bodies through the fake transport — OpenAI-shaped,
     Ollama-shaped, a tool call split over chunks, two parallel tool calls, a 429, a
     truncated stream, and a model the plan does not allow.
   - **`Complete` is unchanged on the wire.**
2. **Config** (S7):
   - `openai-compatible` is accepted as a provider and normalised to the same
     implementation. Keep reporting the provider name the operator set.
   - `AI_API_KEY` is optional when `AI_BASE_URL` is set.
   - `AI_BASE_URL` may be `http://` when its host is loopback, private (RFC 1918 or
     ULA) or a single-label name. Use netguard's classifiers where they fit, and do not
     resolve DNS at boot.
   - A public `http://` host is refused, and so is a key with any `http://` URL. Each
     refusal is a boot error naming the variable.
   - `AI_TIMEOUT` (Go duration, default `120s`, at most `10m`) is `Chat`'s budget.
     `Complete` keeps its 30 s.
   - Tests for each rule.
3. **The assistant grant** (S3):
   - **Migration** `000NN_identity_assistant_grants.sql` (the next free number when the
     task lands; `main` is at 00101 as of writing):
     `identity.assistant_grants(id uuid PK, user_id uuid NOT NULL, session_id uuid NOT
     NULL → the sessions table ON DELETE CASCADE, conversation_id uuid NOT NULL, run_id
     uuid NOT NULL, created_at, expires_at, revoked_at NULL)`, with an index on
     `user_id`.
   - **There is no secret token.** The grant travels as a context value, and its id
     alone is not a credential, because no network input can set it.
   - **The contract.** `contracts.AssistantGrants` is a new single-provider slot,
     provided by identity:
     - `Issue(ctx, conversationID, runID uuid.UUID, ttl time.Duration) (uuid.UUID,
       error)` runs with a session request in ctx, binds the grant to that session,
       and is refused without one.
     - `Revoke(ctx, grantID)`.
     - `RevokeForUser(ctx, userID)`.

     `contracts.WithAssistantGrant(ctx, id)` and `AssistantGrantFrom(ctx)` follow
     `AsUser`'s pattern: an unexported key.
   - **`Principal` gains `Via *Via`**, where `Via{Kind string; ConversationID, RunID
     uuid.UUID}`. It is nil for every other principal.
   - **`identity` `Access.Check`:** when the request's context carries a grant, ignore
     cookies and load the grant with its session. Then:
     - refuse when it is expired, revoked, its session ended, or its user is inactive
       or locked;
     - otherwise answer the session's principal (user, session, roles, MFA state) with
       `Via` set, and evaluate the rule exactly as for that session;
     - additionally refuse with 403 any `policy:` rule naming `Owner`,
       `OwnerManagement`, `AuthorizationManagement` or `SystemAdmin`, and any `scim`
       rule. This is defence in depth beside S4.
   - **Tests:** every refusal; the principal's equality with the session's; a grant
     never accepted from a header or a cookie; signing out ends it.
4. **The `Operations` dispatcher** (S2, S4), in a new platform package
   `internal/operations`:
   - **Built in Compose** from the merged contract of the enabled modules and
     `Deps.Catalog`. It holds one entry per operation: id, method, path template, tag,
     summary, description, the access rule, the **effective mode** (S4's three steps,
     computed once; test every branch), the parameters, and the request and response
     schemas.
   - **`List(ctx, userID) []Info`:** the entries whose mode is not `never` and whose
     rule the user meets.
     - `permission:` rules go through `Access.UserHolds`.
     - `policy:ActiveAccount` and `policy:Business` are listed. The call decides;
       `Business`'s MFA is the session's.
   - **`Search(ctx, userID, query, limit)`:** simple token scoring over summary,
     description, tag and path within `List`. No dependencies.
   - **`Describe(id) (json.RawMessage, error)`:** a compacted JSON Schema view of the
     parameters, request body and success response:
     - `$ref`s resolved to a depth limit (cycles cut with a note);
     - enums inline;
     - descriptions kept;
     - at most 8 KB, truncated with a note.
   - **`Call(ctx, CallRequest{OperationID, PathParams, Query, Body, Confirmed bool})
     (CallResult{Status int; Body json.RawMessage; Truncated bool}, error)`:**
     - refuses `never`, and refuses `confirm` unless `Confirmed`. Both are typed
       errors with stable codes.
     - builds an in-process `*http.Request` for the filled path template, carrying the
       grant from ctx (`AssistantGrantFrom`; no grant is an error) and a `RemoteAddr`
       naming the assistant and the user, so per-client rate limits are per person
       (check what `ratelimit` keys on and make it hold).
     - serves it through the composed API handler (Compose sets the handler on the
       dispatcher once it exists).
     - records into a capped recorder (1 MiB). A JSON body larger than the cap answers
       `Truncated`.
     - answers the status and the body. A 4xx or 5xx is a result, not an error, so
       the model can read the refusal.
   - **Only `assistant` receives it.** `module.Deps` gains `Operations
     *operations.Dispatcher`, which Compose sets only for the module named
     `assistant`. Add a depguard rule: only `internal/assistant`, `internal/module` and
     `internal/server` (and tests) may import `internal/operations`.
   - **Tests** through the real router with a test module and a real identity (use
     `modtest` the way existing cross-module tests do):
     - a permission the user lacks answers 403 through `Call`;
     - a row-level refusal stays a refusal;
     - `never` and unconfirmed `confirm` are refused before dispatch;
     - `List` hides what the user cannot call;
     - a call without a grant is refused.
5. **The contract** (S4, S15):
   - **Lint:** `x-vantigo-assistant`, when present, is one of `auto`, `confirm` or
     `never`. Every operation has a non-blank `summary`. Add tests in `lint_test.go`.
   - **Write the missing summaries** in every `openapi/*.yaml`. Write them from what
     the handler does, short and in the house voice ("List the customer's metering
     points"). Communications and identity lack most of theirs.
   - **Mark `x-vantigo-assistant: auto`** on every `POST` that only reads (search,
     preview, lookup, check). Read the handler before marking one. Mark `never` on
     anything you judge must never be called by an assistant that S4's rules do not
     already exclude, and list each one in the report.
   - **Regenerate:** `go generate` in `apps/server` and `bun run gen:client`. The API
     reference regenerates from the contract; never hand-edit it.
6. **The rule and the documentation:**
   - **Module boundaries:** a new numbered rule, after the last one, saying what S2
     says. `Deps.Operations` and the `AssistantGrants` slot go in the slot tables.
   - **`platform-packages.md`:** `ai` (`Chat`, streaming, local models) and
     `operations`.
   - **`admin/configuration.md` (en and nb):**
     - `AI_PROVIDER=openai-compatible`;
     - the empty key with `AI_BASE_URL`;
     - the `http://` rule;
     - `AI_TIMEOUT`;
     - a worked Ollama example: `AI_BASE_URL=http://ollama:11434/v1`,
       `AI_MODELS=qwen3:14b` or similar, a note that the model must support tool
       calling, and a note that the data stays on the installation.
   - **`admin/authentication.md` (en and nb):** assistant grants, in a short section:
     bound to a session, ended by signing out or expiry, never a credential a client
     holds.
   - **`ROADMAP.md`:** the Assistant, with this delivery done and A to C to come.

## Do not

- Add the `assistant` module, its schema, endpoints or frontend. That is A.
- Add an Anthropic provider.
- Change `Complete`'s behaviour.
- Let a grant be presented by any network input.
- Give `Operations` to any other module.
- Hand-edit the generated API reference.
- Rename an operationId.

## How to work

- **Branch.** You start in the worktree
  `/Users/anders/projects/vantigo-io/vantigo/.claude/worktrees/ai-assistant-platform-41056f`
  on branch `claude/ai-assistant-platform-41056f`, whose single commit beyond
  `origin/main` is the spec and these briefs. Create `feat/assistant-platform` from
  that HEAD (`git switch -c feat/assistant-platform`), so the spec lands with this
  delivery.
- **Setup.** Run `bun install --frozen-lockfile` first, and `mise run server:db` for the
  test database.
- **Plan first:** `docs/superpowers/plans/2026-11-01-assistant-platform.md`, in the
  house shape:
  - `> For agentic workers`;
  - **Goal**, **Architecture**, **Spec**;
  - *Plan readings* with verified file paths and line numbers;
  - *Tasks* as checkboxes, one commit each.

  See `docs/superpowers/plans/2026-10-31-ai-platform.md` for the shape. List the
  documentation pages, en and nb, as their own task. Commit the plan alone, with **no
  `Docs-Impact` trailer**: a `none` trailer anywhere in the range waives the coverage
  check for the whole pull request.
- **Commits.** One per task, Conventional Commits (`feat(ai): …`, `feat(identity): …`,
  `feat(operations): …`, `docs(…): …`), each ending with the line
  `Co-Authored-By: Claude Sonnet 5.5 <noreply@anthropic.com>`.
- **Signing.** Commits are SSH-signed automatically through 1Password. If signing
  fails, stop and report. Never pass `--no-gpg-sign`. Use `--no-verify` only as
  `frontend-tests-node-26` allows: when the husky hook trips over files that are not
  yours.
- **The gates, all green before you push:**
  - `mise run server:check`
  - `mise run server:test`
  - `mise run frontend:check`
  - `mise run docs:check`

  Read each log rather than trusting the wrapper's exit code: `frontend:check` has
  exited 0 on a Biome failure before. A local failure confined to
  `apps/projects/frontend/src/pages/project-tasks.test.tsx` under Node 26 is a known
  local quirk. Run the remaining packages by hand and say so.
- **Keep the branch rebaseable.** Never merge `main` into it. If `main` takes your
  migration number, renumber in your own commits before pushing.
- **The pull request.** `git push -u origin feat/assistant-platform`, then
  `gh pr create --base main` with:
  - a Conventional title (`feat: the Assistant's platform — chat, grants and the
    operations dispatcher`);
  - a body: what and why, linking the spec, the decisions it delivers, the summaries
    and marks you added (counts per contract and the `never` list), the gates' results,
    and a test plan;
  - the body ending with `🤖 Generated with [Claude Code](https://claude.com/claude-code)`.

  Do not wait for CI or poll it; the session that started you watches it.

## Report

- The PR URL and branch.
- `git log --oneline origin/main..`.
- The four gates' tails, verbatim.
- The plan's path and the migration number.
- The operations you marked `never` or `auto`, and why.
- Departures from the spec, with the reason for each.
- Anything delivery A must know: the exact Go API of `Operations`, `AssistantGrants`
  and `ai.Chat`.
