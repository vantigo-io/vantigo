# Agents A2: the agent at work

Design: `docs/superpowers/specs/2026-11-08-virtual-employees-design.md` §5 (V11's placement,
V13 to V19, V22 to V25) and the A1 plan. Stacked on A1 (`agents/a1-module`). The review queue's
screens and decisions (approve, edit, discard), the calibration, the Usage and Activity screens, the
retention and erasure sweeps and the *Give back* button are A3.

## Tasks

1. **Communications** (in its module, migration `00145`): `conversation_messages.agent_id`, set from the
   principal of an agent grant; the `X-Vantigo-Agent` header (`mail.Outbound.Headers`, written by the outbox
   from the stored column) and `agent` in a Messenger message's metadata; `capabilities` on the
   conversation detail; a reply through an agent grant refuses reply-all (422 `reply_all_not_allowed`),
   answers 409 `conversation_taken` when the conversation is assigned to someone else, and refuses the
   `human_agent` window; a third outbox event, `communications.conversation.replied`; `channelId` on
   `assigned`.
2. **Schema** (`00146`): `run_queue`, `handed_off`, `runs`, `run_messages`, `actions`, `review_items`
   (defined in full for A3), `usage_days`, `channel_addresses`, `seen_senders`, and
   `roles.complaint_tag_ids`.
3. **The trigger** (`trigger.go`): the `agents.trigger` handler of the three events; coalescing and
   debounce; the hand-off marker and its clearing.
4. **Runs** (`runner.go`, `worker.go`, `run.go`): the API runner (poll 5 s, claim with `SKIP LOCKED`,
   `AGENTS_CONCURRENCY`, 5-minute ceiling, claim renewal), the `agents-runs` worker (lapse, stale),
   grants and the run's customer (V9b), the gate narrowed to the run's role.
5. **The loop** (`tools.go`, `reply.go`, `prompt.go`): agentloop with the role's caps; `reply`, `note`,
   `tag`, `hand_off`; the pins; the changes always queued.
6. **The pipeline** (`pipeline.go`): V17, S1 to S7, C1 and C2 through `agents.check`, the modes, the
   disclosure.
7. **Budget** (`usage.go`), **hand-off** (`handoff.go`) and the endpoints for the hand-off.
8. **Tests:** table tests per rule and mode; the trigger, claim, lapse, marker and budget against the
   module's tables; the pins; Communications' marking and refusals; integration against a scripted
   model (Review, Auto, spoofing, strangers, injection, hand-off and give back, `ai_unavailable`, a
   pool of one).
9. **Documentation**, both languages where the section is bilingual: `user/agents.md`,
   `admin/agents.md`, `reference/agents.md`, `user/communications.md`,
   `reference/communications.md`, `admin/configuration.md` (`AGENTS_CONCURRENCY`); then `go generate`
   and the regenerated embedded documentation.

## Deviations

- **The conversation's own detail carries no customer scope.** `getCommunicationsConversationsById`
  declared `resource: customerId`, which refuses a run with no customer and a conversation not linked
  yet: an agent could not read the conversation it answers, in the commonest case. The scope is gone
  (and `customerDataOperations` no longer lists it); the operation is pinned to the run's own
  conversation by the tool layer, which is the control that matters here.
- **The run's role is read from the run, not the grant.** The gate looks the run up in `agents.runs`
  (cached for good) rather than adding a column to `identity.agent_grants`; a grant whose run the module
  did not make keeps the union of the agent's roles' allowlists.
- **`Dispatcher.List` does not ask `UserHolds` for an agent's grant.** `UserHolds` is false for a user of
  kind agent by design, which left an agent an empty list. The gate decides what is listed; the router
  still judges the permissions on the call.
- **A lapsed run is a new run.** The `agents-runs` worker stops the lapsed run and writes a retry request
  (`attempt` 2); the second lapse hands off. The design's `queued` state is unused.
- **A hand-off without a dispatcher.** The worker, the trigger and the budget write the marker and a run
  handed off with no outcome; the runner completes the note and the assignee within five seconds.
- **A third event.** A person's reply clears the hand-off marker, and no event said a reply was made:
  `communications.conversation.replied` (`byAgent`) is added, and `assigned` carries `channelId` so the
  trigger can find the role.
- **Attachments are refused by the `reply` tool** (`attachments_unavailable`): a Vantigo document cannot
  be fetched under the customer's scope yet. S2 is coded and tested, with the attachment count as input.
- **S4 (first contact)** is judged from the senders the module has met (`seen_senders`), since
  Communications has no filter by participant. A sender new to the module is a first contact: the safe
  direction. Another channel of the installation (V23) is likewise known only from the channels a run has
  read (`channel_addresses`).
- **`dailyTokenBudget`** is settable on the agent update (0 removes it), and `complaintTagIds` on the role
  update; their screens are A3's.
- **The reviews note links `/agents/review/<id>`** as the design says; the screen is A3's.
