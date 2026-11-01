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

T (tours), B (writes) and C (bulk) are briefed after A lands.

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
