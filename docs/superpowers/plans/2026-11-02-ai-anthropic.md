# Anthropic as an AI provider, with preserved thinking in the Assistant: implementation plan

> **For agentic workers:** implement this plan task by task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** `AI_PROVIDER=anthropic` runs every AI task on Claude: communications' `Complete`
and the Assistant's `Chat`, streamed, with tool calls, confirmations, bulk and tours. The
Assistant keeps each conversation append-only so Claude's preserved thinking never
invalidates: the system prompt is frozen per conversation, what changes per message
travels in the user turn, each assistant turn's provider blocks are stored and replayed
verbatim, and an oversized conversation is compacted by a summary, never edited in place.

**Spec:** [`docs/superpowers/specs/2026-11-01-assistant-design.md`](../specs/2026-11-01-assistant-design.md), S6, S7, S14, S18.
**Brief:** [`…/2026-11-01-assistant-briefs/e-anthropic.md`](2026-11-01-assistant-briefs/e-anthropic.md).
**Reference:** the bundled Claude API reference (Go SDK, streaming, tool use, error codes,
model migration: preserved thinking, fallbacks).

## Plan readings

1. **`internal/ai`.** `New` switches on `cfg.AIProvider` and builds a `chatProvider`
   (`complete`, `chat`). OpenAI's guard is its own `http.Client` over `Deps.HTTPTransport`
   that refuses redirects; typed errors are `aiTransportError`, `aiStatusError`,
   `aiDecodeError`, `aiEmptyResponseError`, whose `%T` names are audit labels.
2. **Config.** `aiConfig` validates provider, key, base URL, timeout and models;
   `redact.go` redacts the key; `AILocal` drives the Assistant's data notice.
3. **The Assistant's run.** `runner.execute` (shared by `SendAssistantMessage`, a decided
   confirmation and a bulk job's resume) rebuilds the system prompt each run with the date
   and the chip, rebuilds the model transcript from `assistant.messages`, shortens older
   tool results in place in memory (`fitContext`) and calls `Chat` with the tools; on the
   last turn it passes no tools.
4. **Thinking.** On Claude, thinking blocks are bound to the exact `system`, `tools` and
   earlier messages that produced them. Rebuilding any of them invalidates every later
   block; accounts created from 2026-08-31 get a 400. A request that carries a
   `tool_use` block must also define `tools`, so the last turn cannot drop them: it sends
   `tool_choice: none` instead.
5. **Fallbacks.** `fallbacks: "default"` under beta `server-side-fallback-2026-07-01` is
   accepted by Opus and Sonnet; Haiku 5.5 rejects any `fallbacks`.
6. **Beta namespace.** Fallbacks and `thinking.block_binding` are beta features: the
   request goes through `client.Beta.Messages`.
7. **Migrations.** `main` is at `00105`; delivery D takes `00106`, this takes `00107`.

## Tasks

- [ ] **1. Config.** `AI_PROVIDER=anthropic` (key required, https base URL only),
  `AI_EFFORT`, `AI_FALLBACKS`; `AILocal` is false for Anthropic. Tests for each rule.
- [ ] **2. The provider-neutral types.** `Message.Provider`, `ChatResponse.Message.Provider`,
  `ChatRequest.ToolsDisabled`, `ChatResponse` cache counts, `ProviderAnthropic`, a typed
  refusal error and a typed max-tokens error.
- [ ] **3. `anthropic.go`.** `Complete` and `Chat` over the official Go SDK behind the
  package's guard; streaming, accumulation, parallel tools, schema validation of every
  tool input, adaptive thinking with `output_config.effort`, `drop_block`, fallbacks,
  prompt caching, error mapping. Tests through a fake `RoundTripper` serving canned SSE.
- [ ] **4. Migration `00107` and queries.** `messages.provider jsonb`;
  `assistant.conversation_state` (frozen prompt and version, compaction summary and the
  sequence it covers).
- [ ] **5. Frozen prompt and per-message header.** `systemPrompt` loses the date and the
  chip; `turnHeader` puts them in each user turn from the stored time and context. The
  prompt is stored at the conversation's first run and reused.
- [ ] **6. Provider blocks stored and replayed.** `saveAssistant` stores them;
  `modelMessages` replays them; the last turn keeps the tools and disables them.
- [ ] **7. Simple compaction for Anthropic.** At the start of a run (never mid tool
  round) past `AI_CONTEXT_TOKENS`, summarize, store the marker, send summary plus the
  later turns. `fitContext` stays for the OpenAI-compatible provider.
- [ ] **8. Identity and the interface.** The provider enum gains `anthropic`; the AI
  models page and the Assistant's notice name "Anthropic".
- [ ] **9. Live test**, gated by `ANTHROPIC_API_KEY`.
- [ ] **10. Documentation, en and nb:** `admin/configuration.md`,
  `user/workspace-administration.md`, `reference/assistant.md`, `platform-packages.md`,
  `ROADMAP.md`.
- [ ] **11. Gates.** `server:check`, `server:test`, `frontend:check`, `docs:check`,
  `gen:client:test`, `gen:client` and `go generate ./...` with no diff, the host build,
  `go test ./cmd/vantigo/`.
