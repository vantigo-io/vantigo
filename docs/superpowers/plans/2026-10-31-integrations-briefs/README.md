# Integrations design — the delivery briefs

Four self-contained briefs, one per delivery of
[the design](../../specs/2026-10-31-integrations-and-configuration-tiers-design.md)
(`862890cb`). Each is the whole prompt an agent needs to plan and build one delivery
from a cold start: what to read, what to build, what not to touch, how to prove it,
how to commit, and what to report. They are written for **Claude Sonnet 5.5**
(`model: sonnet`) and assume the agent has the repository's tools and nothing else.

| Brief | Delivery | Branch | Depends on | Touches |
| --- | --- | --- | --- | --- |
| [A](a-workspace-integrations.md) | Integrations section, Entra in four tabs, `A/configuration.md` | `feat/workspace-integrations` | nothing | host frontend, Entra frontend, docs |
| [B](b-outbound-mail-slot.md) | `contracts.OutboundMail`, the fallback's end, Invoices' and Calendar's sending setting | `feat/outbound-mail-slot` | nothing | Go (communications, invoices, calendar, contracts, module), two migrations, frontends, docs |
| [C](c-hosted-mail-placeholder.md) | *Vantigo hosted mail* shown greyed, "Not yet available", in the channel form | `feat/hosted-mail-placeholder` | **B merged** | Communications frontend, docs |
| [C′](c-prime-hosted-mail-deferred.md) | **Deferred (2026-11-01).** The hosted mail service itself: platform client, `hosted` provider, `MAIL_DRIVER=vantigo`, events webhook | `feat/hosted-mail` | its own research and design; the service's first release | Go (hostedmail, mail, communications, config), docs |
| [D](d-ai-platform.md) | `srv/ai`, tasks per module, identity's `ai_task_models`, the *AI models* page | `feat/ai-platform` | **A merged** (the section) | Go (ai, identity, communications, module, contracts, config), one migration, host frontend, docs |

**Order.** A and B may run at once, each in its own worktree. D starts once A is on
`main`; C once B is. C′ does not start until the service has been researched, designed
and released, and the owner says so. Anders
merges each pull request himself; open each against `main` only after the previous one
it depends on has landed (stacking a delivery on another's branch has sent a delivery
into its base branch before).

**Starting one.** From a session on the repository:

```
Agent(subagent_type: "general-purpose", model: "sonnet",
      prompt: <the whole text of the brief file>)
```

or hand the brief's path to a fresh Claude Code session with "follow this brief".
Give the agent one delivery; do not combine two briefs in one run.

**What every brief shares** (repeated inside each so it stands alone): a worktree cut
from `main`, `bun install --frozen-lockfile` first; the plan written before the code, in
the house shape; the documentation in both languages in the same commit series; the four
gates; Conventional Commits, signed; no push, no pull request, a report at the end.
