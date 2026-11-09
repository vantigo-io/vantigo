# Virtual employees, delivery D: colleagues talk to agents (V31, V32)

Design: `docs/superpowers/specs/2026-11-08-virtual-employees-design.md` §8. Branch `agents/d-colleagues`, stacked on
`agents/c-roles`. Reuses the Assistant's conversations, runs, cards and confirm path, `srv/agentloop`'s dispatcher tools,
the agents' allowlists and levels, and the A2 trigger and run queue.

## Tasks, in order

1. **Mentions in Communications (V32).** Migration `00152` (`conversation_message_mentions`); `mentions` on `NoteRequest`
   (up to ten ids, each a known, active user, the author left out, none for a note written through an agent grant); the note
   in one transaction with its mentions and one `communications.conversation.note_mentioned` event (ids, no text); a mail to
   each person mentioned through `UserDirectory.Notify` once it is committed.
2. **The trigger wakes the agent.** `agents.trigger` takes the event: an active agent with its role on the conversation's
   channel gets the conversation's request, due now, with `mention:<message id>` as its note; not for an agent's own note,
   a handed-off conversation, a paused agent or an agent with no role on the channel. The run reads the note from the
   conversation, shows it as `colleagueNote` (data) and the prompt says a colleague asked; it goes on whoever holds the
   conversation.
3. **The slot `contracts.AgentColleagues`.** `Agents`, `Agent`, `Names`, `Operations`; the agents module provides it
   (`agents/colleagues.go`), Compose resolves it once the dispatcher exists, `modtest.WithAgentColleagues` presets one.
4. **The panel's run (V31).** Migration `00153` (`assistant.conversations.agent_id`); `GET /assistant/agents`, `agentId` on
   `POST /conversations`, `agent` on a conversation; the run stays the person's (assistant grant) and `colleagueOps` narrows
   the dispatcher to what the agent may use too; the prompt carries the persona; no bulk change; the card's confirmation
   rechecks `agents:view`, the agent and its level. The rule for who may: `agents:view`.
5. **Frontend.** Assistant: the picker on a draft, the agent's name and AI badge on its conversations, the error for an
   agent that is gone. Communications: the @ picker in the note composer, `mentionsIn` (the ids whose @Name is still in the
   text), the ids sent with the note.
6. **Documentation** (English and Norwegian where the section is bilingual):
   - `user/assistant.md` (en, nb): talking to a virtual employee;
   - `user/communications.md` (en, nb): mentioning a colleague in a note;
   - `user/agents.md` (en, nb): the two ways to ask an agent for help;
   - `reference/agents.md`: mentions, the colleagues slot, the trigger's fourth event;
   - `reference/assistant.md`: the schema, the endpoints, the conversations with an agent, the prompt version;
   - `reference/communications.md`: the event and the mentions in notes;
   - `contributing/module-boundaries.md`: the slot.
   Then `go generate ./...` in `apps/server` (the Assistant embeds the docs).

## Tests

Mentions: stored ids and the one event without text, the mail to a person and not to an agent, the refusals (unknown,
disabled, more than ten), no mention from an agent's note, nothing for a note without mentions or a missing conversation.
Trigger: the request due now with the note's id, nothing for a person, another channel, a paused agent, a handed-off
conversation or an agent's own note; a mention meeting a waiting message request; an end-to-end run that starts with the
colleague's note and answers with a note. Slot: Compose's. Panel (against the real agents, Communications, Customers and
Assistant): the picker's permission and a paused agent; the persona and no bulk tool; a change both may make is a card and
nothing runs before the confirmation; the agent may and the person may not; the person may and the agent may not; no
customer data though the person may read it; a card refused once the agent's level was lowered. Frontend: the picker, the
header, `mentionsIn`, the @ select.

## Deviations

- The panel's run is the **person's** (an assistant grant), narrowed by the agent, not an agent grant checked again as the
  person: the router then judges the person on every call without a second mechanism, and the agent's side is the pure
  question `AgentColleagues.Operations` answers. The audit is the person's own, in the Assistant's action log.
- A customer named by the screen is not read in the panel: a panel run has no customer, so the operations that declare a
  customer scope are never offered (the design allows one when the message names a customer the person may read; not built).
- The @ picker is a select above the note's editor that inserts `@Name`, not a live `@` autocomplete inside the editor,
  which would add the Tiptap mention extension to the Communications app.
- A mention's request keeps the reason `message` and carries the note's id in its `note`, to avoid a migration of the reason
  check on `run_queue` and `runs`.
- The agent's own model per agent (`agents.run`) is not used in the panel: it is the Assistant's model, and the person's
  token budget counts.
