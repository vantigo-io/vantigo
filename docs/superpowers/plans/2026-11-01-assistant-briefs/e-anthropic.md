# Brief E: Anthropic as an AI provider

You are implementing decision **S18** of
`docs/superpowers/specs/2026-11-01-assistant-design.md`. Read the whole spec first (S6,
S7, S14 and S18 especially), then this brief. Deliveries 0, A, B, T and C are on
`main`. Model: Claude Sonnet 5.5.

## The outcome

An operator sets:

```
AI_PROVIDER=anthropic
AI_API_KEY=sk-ant-...
AI_MODELS=claude-opus-5-5,claude-sonnet-5-5,claude-haiku-5-5
```

and every AI task runs on Claude:
- communications' reply drafts and customer suggestions, through `Complete`;
- the Assistant, through `Chat`, with streaming, tool calls, confirmations, bulk and
  tours, as with OpenAI.

The Owner picks a Claude model per task under *AI models*. The Assistant's notice says
the data goes to Anthropic. On accounts that enforce preserved thinking, nothing
breaks.

## Read before you plan

- **The Claude API reference bundled with this session**, under
  `/private/tmp/claude-501/bundled-skills/2.1.295/8bc0138faadbc551cd5e4ebff8dac0ba/claude-api/`.
  It is authoritative over anything you remember: the API changed in 2025–2026. Read:
  - `go/claude-api/README.md`, `streaming.md`, `tool-use.md`: the official Go SDK
    `github.com/anthropics/anthropic-sdk-go`;
  - `shared/tool-use-concepts.md`: tool results, parallel tool calls, eager input
    streaming;
  - `shared/prompt-caching.md`;
  - `shared/error-codes.md`: the Go `errors.As` pattern;
  - `shared/model-migration.md`, the sections **Migrating to Claude Opus 5.5** and
    **Migrating to Claude Fable 5.1 from Claude Fable 5 → Breaking change 3** (preserved
    thinking: what invalidates a thinking block, `drop_block`, and simple compaction);
  - the refusal and fallbacks sections it points to.

  If those files are gone, re-fetch the SDK's own docs from
  <https://github.com/anthropics/anthropic-sdk-go> with WebFetch. **Never guess an SDK
  name.** Write the code, compile it, and let the compiler correct the names.
- **The repository:**
  - `AGENTS.md`, `platform-packages.md` (`ai`);
  - `apps/server/internal/ai/`, all of it, especially the package comment's "real
    client, fake transport" argument, which must stay true;
  - `apps/server/internal/config/config.go`: the `AI_*` block and its validation, and
    `redact.go`;
  - **the Assistant's use of `Chat`:** `apps/server/internal/assistant/runs.go`,
    `prompt.go` (the system prompt is rebuilt per run today, with the date and the
    context chip), `compact.go` (S7's in-place shortening of older tool results), and
    how messages are persisted (`queries/`, the `messages.content` parts);
  - communications' `ai.go` (`Complete`);
  - identity's AI models page contract (`openapi/identity.yaml`, the provider enum),
    and `apps/host/frontend/src/routes/workspace/-ai-models.tsx`.

## Build

1. **Config.**
   - `AI_PROVIDER` accepts `anthropic`. With it, `AI_API_KEY` is required, and
     `AI_BASE_URL` is optional (`https` only).
   - `AI_EFFORT` (`low` | `medium` | `high` | `xhigh` | `max`, default `medium`) and
     `AI_FALLBACKS` (`on` | `off`, default `on`) are read for every provider and used
     by Anthropic. Document that the other providers ignore them.
   - Tests for each rule. The key stays redacted.
2. **The provider, `internal/ai/anthropic.go`,** on the official Go SDK (add it to
   `go.mod`; govulncheck must stay clean).
   - **The guard:** pass `option.WithHTTPClient` with the package's client
     (`Deps.HTTPTransport`, redirects refused), `option.WithAPIKey`, the base URL when
     set, and **SDK retries off**. The context carries the budget.
   - **`Complete`:** one user message, non-streaming, the same typed errors as
     OpenAI's.
   - **`Chat`, streaming:**
     - map `ChatRequest` to Messages params: the system as a text block, the messages
       as typed content blocks (`tool_use` and `tool_result`), and the tools as
       `ToolParam`s with `eager_input_streaming` set as the reference recommends;
     - several tool results of one turn go back in **one** user message;
     - stream text deltas to `onDelta`;
     - accumulate the final message;
     - map `stop_reason`, the token counts and the cache read and write counts;
     - **validate every tool input** against its schema before returning it (eager
       streaming can truncate);
     - `max_tokens` and `refusal` become typed errors (`aiRefusalError` carries the
       category).
   - **Thinking:** leave it adaptive. Never send `budget_tokens` or `disabled` (current
     models refuse both). Send `output_config.effort` from `AI_EFFORT`. Thinking text
     is never shown.
   - **Fallbacks:** when `AI_FALLBACKS=on` and the model supports them, send
     `fallbacks: "default"` with beta `server-side-fallback-2026-07-01`. Read which
     models do from the reference.
   - **Prompt caching:** mark the stable prefix (tools, then system) for caching, as
     the reference shows.
   - **Errors:** `errors.As` into the SDK's error type, mapped onto the package's typed
     errors (status, transport, decode, empty) with the same labels.
   - **Tests** through a fake `RoundTripper` serving canned Messages SSE:
     - text only;
     - a tool call whose input arrives as `input_json_delta` chunks;
     - two parallel tool calls;
     - thinking blocks present;
     - `refusal`; `max_tokens`; a 429 and a 529;
     - a stream cut before `message_stop`;
     - cache counts;
     - a model outside `AI_MODELS` refused before any request.

     Assert the request body the SDK sent (system, tools, `cache_control`, effort,
     fallbacks, the beta header).
3. **Preserved thinking in the provider-neutral types:**
   - `ai.Message` gains `Provider json.RawMessage`, opaque. For an Anthropic assistant
     turn it is the turn's full content blocks exactly as received (thinking blocks
     with signatures, text, `tool_use`). For OpenAI it stays empty.
   - `ChatResponse.Message` carries it, and `Chat` replays it **verbatim** when it is
     present, instead of rebuilding the turn from Text and ToolCalls.
   - **Send `thinking.block_binding.prefix_mismatch_behavior: "drop_block"`** with beta
     `thinking-binding-controls-2026-08-01`. Log every `input_transformations` entry
     (type, reason, path) at warn, without content.
   - **Test it:** a two-turn tool conversation replays the first turn's thinking block
     byte for byte.
4. **The Assistant keeps the history append-only (S18):**
   - **Persist** each assistant message's `Provider` value. Use the next free
     migration number. Delivery D takes `00106`, so take `00107`, and the next free
     number if `main` has moved.
   - **Freeze the system prompt per conversation:** store it on the conversation at
     its first run and reuse it. Move what changes per message (the date, the context
     chip) into a short header on that message's user turn. Keep the prompt version
     on the conversation. A conversation started under an older version keeps its
     frozen prompt.
   - **The tool set** is fixed per conversation (it already is, given T's static
     registry). A test asserts two runs of one conversation send byte-identical
     `system` and `tools`.
   - **Compaction for Anthropic: simple compaction.** When the history passes
     `AI_CONTEXT_TOKENS`:
     - summarize the conversation so far into one message, using the same model and
       the reference's advice on what the summary must keep;
     - store the summary as a compaction marker on the conversation;
     - from then on, send the summary plus the turns after it;
     - **never shorten a stored turn in place** when the provider is Anthropic. Keep
       S7's in-place shortening for the OpenAI-compatible provider.

     Never compact in the middle of a tool round.
5. **Identity's AI models page:** the provider enum gains `anthropic`, and the page
   names it "Anthropic". The Assistant's availability marks it not local, so the notice
   says the data goes to Anthropic.
6. **Documentation** (en and nb where bilingual):
   - **`admin/configuration.md`:**
     - `AI_PROVIDER=anthropic` with a worked example;
     - `AI_EFFORT`, `AI_FALLBACKS`;
     - which model ids to put in `AI_MODELS`;
     - that data is sent to Anthropic, and the operator's data processing agreement.
   - **`user/workspace-administration.md`:** the *AI models* page names Anthropic.
   - **`platform-packages.md`:** `ai`'s two providers, the opaque `Provider` field,
     and the guard over the SDK.
   - **`reference/assistant.md`:** the frozen system prompt, the per-message header,
     simple compaction for Anthropic, `drop_block`, and the logged transformations.
   - **`ROADMAP.md`.**
7. **A live test,** gated by `ANTHROPIC_API_KEY` and skipped without it: a short
   two-turn tool conversation against `claude-haiku-5-5`. It asserts the stream and the
   tool call, and that the second request's `input_transformations` is empty. Run it
   if a key is available in your environment, and say whether you did.

## Do not

- Write raw HTTP to Anthropic, or use an OpenAI-compatible shim for Claude.
- Send `budget_tokens`, `thinking: disabled`, sampling parameters, an assistant
  prefill or a forced `tool_choice`. Current models refuse each one.
- Rebuild the system prompt or the tool list inside a conversation.
- Edit stored turns in place for Anthropic.
- Let an Owner enter a key (T14).
- Change OpenAI's behaviour, except through the shared types' new optional field.

## How to work

- **The worktree:**
  `/Users/anders/projects/vantigo-io/vantigo/.claude/worktrees/assistant-anthropic`,
  on branch `feat/ai-anthropic`. Its first commit carries this brief and the spec's
  S17 and S18. Delivery D runs at the same time in another worktree and also touches
  the assistant module, so stay out of the bulk files.
- **Setup:** `bun install --frozen-lockfile` and `mise run server:db` first.
- **Plan first:** `docs/superpowers/plans/2026-11-02-ai-anthropic.md`, in the house
  shape, committed alone.
- **Commits:** one per task, signed, Conventional Commits, each ending with
  `Co-Authored-By: Claude Sonnet 5.5 <noreply@anthropic.com>`.
  - **Never put a `Docs-Impact` trailer on any commit.** Never pass `--no-gpg-sign`.
- **The gates, all green, and rerun whole after any fix:**
  - `mise run server:check` (govulncheck included), `server:test`, `frontend:check`,
    `docs:check`;
  - `bun run gen:client:test`, and `bun run gen:client` and `go generate ./...` with
    no diff;
  - the host build;
  - `go test ./cmd/vantigo/`.
- **CodeQL:** no strings built from untrusted text, no backtracking regular
  expressions, no allocation sizes summed from input.
- **The pull request:** `git push -u origin feat/ai-anthropic`, then
  `gh pr create --base main`:
  - title `feat(ai): Anthropic as a provider, with preserved thinking in the Assistant`;
  - the body ending with `🤖 Generated with [Claude Code](https://claude.com/claude-code)`.

  Do not poll CI.

## Report

- The PR URL and the commits.
- The gates, and whether the live test ran.
- The SDK version.
- The migration number.
- How compaction and the frozen prompt work now.
- Departures.
