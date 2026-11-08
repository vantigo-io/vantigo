# Agents delivery L — the shared loop (plan)

Design: `docs/superpowers/specs/2026-11-08-virtual-employees-design.md`, §3 (V4). One
refactor, no change in behaviour: the Assistant's model loop becomes the platform package
`apps/server/internal/agentloop`. `promptVersion` is not bumped.

## Tasks

1. **The package.** `agentloop/loop.go` (`Loop`, `Input`, `Outcome`, `Caps`, `Tool`, `Result`,
   `Store`, `Emit`, `Run`), `wrap.go` (`Wrap`, `Refused`, `ToolArgs`), `compact.go` and `summary.go`
   (moved from `assistant/compact.go` and `summary.go`: `CompactJSON`, `CompactBody`, `FitContext`,
   `ContextSize`, the summary request), `operations.go` (`Dispatch`: `search_operations`,
   `describe_operation`, `call_operation` over an interface `*operations.Dispatcher` satisfies, with a
   pluggable `Confirm`).
2. **The Assistant's side.** `assistant/loop.go`: `runStore` (messages, actions, tokens, stop, budget),
   `runEmit` (the SSE queue). `runs.go` `execute()` ends in `Loop.Run`; `runTool`'s recording half
   becomes `runStore.AppendResult`. `tools.go`: `toolbox` keeps `search_docs`, `navigate`, `show_tour`,
   `propose_bulk` and the pending flow (`Dispatch.Confirm` is `toolbox.confirm`). Tool lists keep their
   order, so the model is offered what it was.
3. **depguard.** `internal/agentloop` joins the `platform` set and the `operations` exclusion.
4. **Tests.** New: `agentloop/loop_test.go` (a fake AI and an in-memory store: caps, the final turn,
   Anthropic's history, stop, budget, pause, ceiling) and `operations_test.go` (a fake dispatcher). The
   compaction tests move with the code. The Assistant's whole suite runs unchanged.
5. **Documentation** (English only; both pages are `reference/` and `contributing/`, which have no
   Norwegian copy): `contributing/module-boundaries.md` (the new "The model loop" section, rule 18's
   import list; `agentloop` in its `sources`), `reference/assistant.md` ("The model loop"; `agentloop`
   in its `sources`). Then `go generate ./...` in `apps/server` for the embedded documentation.

## Deviations

- `modelMessages` stays in the Assistant: it is the codec of the Assistant's stored rows, not of the
  loop. The loop is handed the history (`Input.History`).
- `TestCompactJSON_…` and `TestFitContext_…` moved to `agentloop` with the functions they test; they
  are unchanged apart from the exported names.
- `Caps` also carries `ContextTokens` and `AnswerTokens`, and `Tool` a `Description`, which the loop
  needs to build a request. The Assistant passes no `Ceiling`: its run's context already carries it
  from before the loop starts.
