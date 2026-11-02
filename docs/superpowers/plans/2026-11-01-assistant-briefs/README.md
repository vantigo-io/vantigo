# The Assistant — delivery briefs

Self-contained briefs, one per delivery of
[the design](../../specs/2026-11-01-assistant-design.md). Each brief is the whole prompt
an agent needs to plan and build one delivery from a cold start: what to read, what to
build, what not to touch, how to prove it, how to commit, how to open the pull request
and what to report. They are written for **Claude Sonnet 5.5** (`model: sonnet`).

| Brief | Delivery | Branch | Depends on | Touches |
| --- | --- | --- | --- | --- |
| [0](0-platform.md) | `ai.Chat`, local models, the assistant grant, the `Operations` dispatcher, assistant modes, summaries | `feat/assistant-platform` | nothing | Go (ai, config, contracts, module, identity, openapi), one migration, contracts, docs |
| [A](a-assistant-app.md) | The `assistant` module, the `/assistant` app, the panel, docs search, read tools | `feat/assistant-app` | **0 merged** | Go (new module), migrations, new frontend package, host, docs (en and nb) |
| [B](b-writes.md) | Changes with confirmation cards, the daily budget, the usage view, safe links | `feat/assistant-writes` | **A merged** | Go (assistant), a migration, assistant frontend, docs (en and nb) |
| [T](t-tours.md) | *Show me*: Mantine Tour over a registry of `data-assist` targets | `feat/assistant-tours` | **A merged**; run after B | module frontends (markers), assistant (Go and frontend), host, docs (en and nb) |

| [C](c-bulk.md) | Bulk changes: a frozen selector, preview, a canary of 5, the job in chunks, failed items | `feat/assistant-bulk` | **B and T merged** | Go (assistant, a migration), assistant frontend, docs (en and nb) |
| [D](d-bulk-once.md) | A bulk item runs at most once: `in_flight`, `unknown` on takeover, an idempotency key where declared (S17) | `feat/assistant-bulk-once` | C merged | Go (assistant, a migration), assistant frontend, docs (en and nb) |
| [E](e-anthropic.md) | Anthropic as an AI provider on the official Go SDK; preserved thinking in the Assistant (S18) | `feat/ai-anthropic` | C merged; merge after D | Go (ai, config, assistant, identity contract, a migration), host frontend, docs (en and nb) |

B, T and C all change the assistant's tools, prompt and panel, so they run one after the
other: B, then T, then C.

**Order.** 0, then A once 0 is on `main`. Anders merges each pull request himself. Open
each one against `main` once the delivery it depends on has landed. Stacking a delivery
on another's branch has sent one into its base branch before.

**Starting one:**

```
Agent(subagent_type: "general-purpose", model: "sonnet",
      prompt: <the whole text of the brief file>)
```

**What every brief shares** (repeated inside each so it stands alone):
- the branch cut from `main` as it is when the delivery starts, and
  `bun install --frozen-lockfile` first;
- the plan written before the code, in the house shape;
- the documentation in both languages in the same commit series;
- the four gates green;
- Conventional Commits, signed;
- the branch pushed and a pull request opened against `main`, with CI green;
- a report at the end.
