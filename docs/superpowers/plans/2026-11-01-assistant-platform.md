# The Assistant, delivery 0 — the platform: implementation plan

> **For agentic workers:** implement this plan task by task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** the floor delivery A's assistant stands on, with no screen of its own. `ai.Chat`
streams a chat with tools against any OpenAI-compatible endpoint (OpenAI, Ollama, vLLM,
LM Studio); `AI_PROVIDER=openai-compatible`, an empty `AI_API_KEY` with `AI_BASE_URL`, an
`http://` base URL on a loopback, private or single-label host, and `AI_TIMEOUT` make local
models configurable; identity issues *assistant grants* and `Access.Check` honours one
carried on a request's context; a platform `Operations` dispatcher lists, describes and
calls the merged contract's operations in-process through the composed router, as the
grant's principal, and only the module named `assistant` receives it; `x-vantigo-assistant`
is linted, every operation has a `summary`, and read-only `POST`s are marked `auto`.

**Architecture:** `config` accepts the new provider, key, URL and timeout rules. `internal/ai`
gains `Chat` beside `Complete` (a streaming Chat Completions implementation in the OpenAI
file, provider-neutral types in `ai.go`). `contracts` gains `Principal.Via`, the
`AssistantGrants` single-provider slot and the grant's context value; identity provides the
slot over `identity.assistant_grants` and `Access.Check` resolves a grant's session in place
of a cookie. `internal/operations` is a new platform package: `New()` returns an unbound
dispatcher that Compose hands to the `assistant` module's `Deps.Operations` before Mount and
binds (`Bind`) to the merged contract, the catalog, `Deps.Access` and the composed handler
once they exist. The contract's lint checks `x-vantigo-assistant` and requires a summary.
Branch `feat/assistant-platform`, cut from `claude/ai-assistant-platform-41056f`
(`origin/main` `319a8e73` plus the design and the briefs).

**Spec:** [`docs/superpowers/specs/2026-11-01-assistant-design.md`](../specs/2026-11-01-assistant-design.md), S2, S3, S4, S6, S7, S15.
**Brief:** [`…/2026-11-01-assistant-briefs/0-platform.md`](2026-11-01-assistant-briefs/0-platform.md).
**Pattern:** [`2026-10-31-ai-platform.md`](2026-10-31-ai-platform.md) for the AI package and config.

## Plan readings

1. **`Complete` and its provider.** `ai/ai.go:84-104` (`New`) returns nil without a
   provider, a key and a model, and builds `newOpenAI(cfg.AIAPIKey, cfg.AIBaseURL, transport)`
   for `ProviderOpenAI`; `Complete` (`:164`) resolves, allows and calls `chat.complete`.
   `openai.go:55` bounds the call at a fixed 30 s, reads the body whole and returns
   `aiTransportError`, `aiStatusError`, `aiDecodeError`, `aiEmptyResponseError`
   (`errors.go`); their `%T` names are audit labels, so `Chat` reuses them. The client
   refuses redirects (`openai.go:40`). `ai_test.go` drives it through `fakeTransport`
   (`:34-60`).
2. **Config.** `config.go:512-537` holds the AI fields; `aiConfig` (`:1228-1326`) requires
   a key (`:1281`), demands `https` (`:1289`) and accepts only `aiProviders = ["openai"]`
   (`:1211`). `redact.go:45` redacts the key. Tests: `config_test.go:1134-1215`,
   `redact_test.go:54`. `netguard.PrivateNetwork` (`netguard.go:218`) classifies RFC 1918
   and ULA addresses; loopback is `netip.Addr.IsLoopback`. Durations are parsed with
   `time.ParseDuration` (`config.go:1078`). Boot must not resolve DNS.
3. **Access.** `contracts/access.go:11` is `Principal`; `AsUser` (`:79`) is the model for an
   unexported context key. `identity/access.go:73` is `Check`; `authenticate` (`:226`)
   reads the cookie through `sessionTokenFrom` and `GetSessionByTokenHash`
   (`queries/sessions.sql:16`), whose predicates `UserMayAct` repeats; `policySatisfied`
   (`:292`) and `permitted` (`:372`) take the session row. A grant needs the same session
   row looked up by id, so a `GetSessionByID` query repeats the same predicates (and the
   comment that the three agree).
4. **Sessions.** `identity.sessions` (`00002_identity_baseline.sql:137`) is the table the
   grant references. The next free migration is `00102` (`origin/main` is at `00101`).
   `identity/sqlc.yaml` lists identity's migrations, and `internal/db`'s schema test keeps
   the list in step.
5. **The router.** `module/router.go:233` (`wrap`) rate-limits on
   `httpx.ClientIP(req)` (`httpx/forwarded.go:85`: the forwarded middleware's value, else
   the host part of `RemoteAddr`, else `RemoteAddr` whole), then `Access.Check`, then
   `WithPrincipal` and `WithRequest`. An in-process request built on a context that
   carries no forwarded value, with `RemoteAddr = "assistant-<user id>"`, is therefore
   rate-limited per person. The request context must not inherit the caller's values.
6. **Compose.** `module/compose.go:138` (`composeFrom`) builds the catalog (`:147-163`),
   resolves the slots (`:200-430`), mounts each module with its own `modDeps` (`:442`),
   computes the merged contract body (`:480`) and returns `dispatchModules(mounts, outer)`
   (`:507`), the composed handler. `Deps` is `module.go:30`; `Module` carries the slot
   funcs. `soleProvider` (`:514`) is the single-provider rule. Workers does not compose.
7. **The contract.** `openapi/lint.go:23` (`Lint`) checks operationId, access and a success
   response; its tests are `openapi_test.go`. `generate.go` regenerates the embedded copy,
   the oapi-codegen output and sqlc; `bun run gen:client` regenerates the TypeScript.
8. **depguard.** `apps/server/.golangci.yml:12-` has one rule per module, scoped by `files`;
   a rule that denies `internal/operations` to every file outside `assistant`, `module`,
   `server`, `operations` itself and the test harness is new.
9. **The harness.** `modtest.New` (`modtest.go:624`) composes identity beside the module
   under test over a migrated database and serves it through `httptest`; `Harness.session`
   (`:991`) creates a session row and returns its token; `Harness.SignIn` (`:906`) a
   client. The composed handler `api` is a local in `New`; the dispatcher's tests need it
   in-process, so the harness keeps it (`API`), and builds a dispatcher over it
   (`Operations`) from the merged contract.
10. **Docs.** `en/contributing/module-boundaries.md` (rule list, slot tables),
    `en/contributing/platform-packages.md`, `admin/configuration.md` and
    `admin/authentication.md` in `en/` and `nb/`, and the repository `ROADMAP.md`.
    `bun run tools/docs/check-coverage.ts --list` prints which pages claim which paths.

## Tasks

- [ ] **1. The plan.** This file, committed alone, with no `Docs-Impact` trailer.
- [ ] **2. Local models in `config` (S7).**
  `openai-compatible` accepted and kept as the operator wrote it; `AI_API_KEY` optional
  when `AI_BASE_URL` is set; `http://` accepted for loopback, RFC 1918/ULA and single-label
  hosts (no DNS), refused for a public host and for any key over `http://`; `AI_TIMEOUT`
  (default `120s`, at most `10m`) as `Config.AITimeout`. Tests for each rule.
- [ ] **3. `ai.Chat` (S6).** Types, the streaming OpenAI-compatible implementation, the
  keyless client, `Complete` unchanged on the wire. Tests with canned SSE bodies: OpenAI,
  Ollama, a split tool call, two parallel tool calls, a 429, a truncated stream, a model
  the plan does not allow.
- [ ] **4. The assistant grant (S3).** Migration `00102_identity_assistant_grants.sql`,
  queries, `contracts.AssistantGrants`, `WithAssistantGrant`/`AssistantGrantFrom`,
  `Principal.Via`, identity's slot and `Access.Check`'s grant branch with its refusals.
  Tests: every refusal, the principal equal to the session's, a grant never accepted from a
  header or a cookie, signing out ends it.
- [ ] **5. The contract (S4, S15).** The lint (`x-vantigo-assistant`, a summary on every
  operation) with its tests; a summary on every operation; `auto` on every read-only
  `POST`, `never` where S4 does not already exclude; regenerate.
- [ ] **6. The `Operations` dispatcher (S2, S4).** `internal/operations` (modes, `List`,
  `Search`, `Describe`, `Call`), `Deps.Operations` and Compose's wiring for the module named
  `assistant` only, the depguard rule, the harness's `API` and `Operations`. Tests through
  the real router and real identity.
- [ ] **7. Documentation.** In one commit, so the coverage check sees the pages together:
  - `contributing/module-boundaries.md`: the new numbered rule, `Deps.Operations` and the
    `AssistantGrants` slot in the slot tables;
  - `contributing/platform-packages.md`: `ai` (`Chat`, streaming, local models) and `operations`;
  - `admin/configuration.md`, `en/` and `nb/`: `openai-compatible`, the empty key, the `http://`
    rule, `AI_TIMEOUT`, an Ollama example;
  - `admin/authentication.md`, `en/` and `nb/`: assistant grants;
  - `ROADMAP.md`: the Assistant, with this delivery done and A to C to come.

  No commit in the range carries a `Docs-Impact` trailer: a `none` anywhere would waive
  the coverage check for the whole pull request.
