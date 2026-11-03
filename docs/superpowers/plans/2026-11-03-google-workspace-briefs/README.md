# Google Workspace design — the delivery briefs

Eight self-contained briefs, one per delivery of
[the design](../../specs/2026-11-03-google-workspace-design.md). Each is the whole
prompt an agent needs to plan and build one delivery from a cold start: what to read,
what to build, what not to touch, how to prove it, how to commit, and what to report.

| Brief | Delivery | Branch | Depends on | Migrations | Model |
| --- | --- | --- | --- | --- | --- |
| [0](0-neutral-seams.md) | Neutral seams: many-provider mailbox slots, Communications and the calendar provider-neutral, the account kind, `sub`, directory search, the host's lists | `feat/google-neutral-seams` | nothing | 00109–00111 | opus |
| [A](a-foundation.md) | The `google` module: connection, key, guarded client, *Test connection*, four tabs | `feat/google-foundation` | **0 merged** | 00112 | sonnet |
| [B](b-accounts.md) | Accounts, groups, licences, photos (`IdentityProvisioner`) | `feat/google-accounts` | **A merged** | 00113 | opus |
| [C](c-link-users.md) | Connect existing users: directory search, matches, links, the SSO card | `feat/google-link-users` | **B merged**, U1 settled | — | sonnet |
| [D](d-connection.md) | The personal Google connection | `feat/google-connection` | **C merged**, U1, U9 settled | 00114 | opus |
| [E](e-calendar.md) | Calendar sync on the connection (`MailboxCalendar`) | `feat/google-calendar` | **D merged** | 00115 | opus |
| [F](f-mail-channels.md) | Gmail mail channels (`ChannelMailbox`) | `feat/google-mail-channels` | **D merged** (the sweep, the tab) | 00116 | opus |
| [G](g-personal-sending.md) | Personal sending through Gmail (`PersonalMailbox`) | `feat/google-personal-sending` | **D and F merged** | — | sonnet |

**Order and the stack (the owner, 2026-11-03).** 0 → A → B → C → D → E → F → G, each
delivery's branch cut from the previous delivery's branch and its pull request opened
with `--base` that branch, so the whole design is reviewable as one stack without
waiting for a merge. GitHub merges a stacked pull request into its *base*, not `main`:
the moment a delivery lands, the next one is retargeted (`gh pr edit <n> --base main`)
before it can be merged, and each description says so. "Depends on … merged" in the
table reads "stacked on" while the stack is open. The migration numbers above are
reservations in delivery order; take the next free number when the migration's task
lands and renumber in-history before a rebase if `main` took it first.

**The unverified items** (the design's U1–U19) are settled in the owner's test tenant.
A brief says which it depends on; its code takes the design's defensive path whether
or not the item is settled, and the report says which were confirmed.

**Starting one.** From a session on the repository:

```
Agent(subagent_type: "general-purpose", model: <the table's>,
      prompt: <the whole text of the brief file>)
```

or hand the brief's path to a fresh Claude Code session with "follow this brief". Give
the agent one delivery; do not combine two briefs in one run.

**What every brief shares** (repeated inside each so it stands alone):

- a worktree on the brief's branch cut from the previous delivery's branch, `bun install --frozen-lockfile`
  first (a stale worktree shows as Biome rejecting `biome.json`);
- the plan written before the code, in the house shape, committed alone;
- the documentation in the same commit series — `user/` and `admin/` in `en` **and**
  `nb`, `reference/` and `contributing/` in English; after any docs edit,
  `go generate ./...` in `apps/server` and the regenerated assistant docs committed as
  `chore(assistant): regenerate the embedded documentation`;
- **no `Docs-Impact: none` trailer on any commit** unless the whole pull request
  changes no documented behaviour — a trailer on any one commit waives the coverage
  check for the whole range;
- the four gates: `mise run server:check`, `mise run server:test`,
  `mise run frontend:check`, `mise run docs:check`;
- Conventional Commits as `git log` shows them (`feat(google): …`,
  `docs(google): …`), SSH-signed through 1Password — if signing fails, stop and report;
- no merge of `main` into the branch; the session that started the agent pushes and opens
  the pull request unless the prompt says otherwise; a report at the end.
