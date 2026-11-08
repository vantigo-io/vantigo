# Agents B: memory and feedback

Design: `docs/superpowers/specs/2026-11-08-virtual-employees-design.md` §6 (V27, V28) and V21's note on Auto items answered
over. Stacked on A3 (`agents/a3-review`). A2 made the runs, tools and pipeline, A3 the review items, calibration, retention and
erasure; B adds what the agent keeps between runs and what the Owner learns from the queue.

## Tasks

1. **Schema** (`00148`): `agents.memory`, `agents.instruction_suggestions`, `runs.answered_over_at`,
   `roles.feedback_checked_at`, `review_items`' original text kept when a reviewer edits (in the item, no column).
2. **Memory** (`memory.go`): the `remember(text)` tool writes a note about the run's customer, or its conversation when the
   run has none (at most 3 notes a run, 500 characters, 50 a subject, the oldest dropped). A run starts by reading up to 20
   notes, newest first: the customer's only when the run's customer is that customer (V9b), and the conversation's own; they
   reach the model inside the data block and the read is logged as a customer-scoped read so S1 treats the reply accordingly.
   `DELETE /agents/memory/{noteId}` (agents:manage, audited without the text) and `GET /agents/agents/{id}/memory` (counts).
3. **The contract** (`contracts.AgentMemory`, a single-provider slot filled by agents, consumed optionally by Customers):
   `GET /customers/{id}/agent-memory` (customers:view + agents:view; empty when agents is off). Module boundaries updated.
4. **Erasure and retention**: memory and suggestions go with the customer, the contact, a deleted agent and
   `retention_months` (`personal_data.go`, `retention.go`).
5. **Feedback** (`feedback.go`, `worker_feedback.go`): the `agents-feedback` worker, once a week in the business time zone,
   groups a role's discard reasons and large edits (ratio over 20 %) since its last check, asks the `agents.check` model for
   at most three edits and stores them in `instruction_suggestions` with the item ids they rest on. The model sees reasons
   and the before/after text of edits, never an id of a customer or a person. `GET /agents/roles/{id}/suggestions`,
   `POST /agents/suggestions/{id}/apply` (If-Match on the role's version; an ordinary role edit, audited) and `/dismiss`.
6. **Answered over** (`trigger.go`, `calibration.go`): a person's outbound reply (`conversation.replied`, not by an agent) in a
   conversation within 24 hours of an Auto-sent reply marks the run `answered_over_at`; calibration counts it as bad, and
   an Auto reply left alone for 24 hours as good.
7. **Frontend**, English and Norwegian: `@vantigo/agents-ui` (suggestions on the role page, memory counts on the agent page)
   and `@vantigo/customers-ui` (the collapsed *What <agent> remembers* section with delete); regenerated API clients.
8. **Tests**: the tool's scope and caps, reading (and never another customer's), erasure, retention, the weekly worker with a
   fake check model, apply and dismiss with If-Match and audit, the answered-over label in calibration, an end-to-end run,
   compose of the new slot, frontend unit tests.
9. **Documentation**: `user/agents.md`, `user/customers.md` (en and nb), `admin/agents.md` (en and nb, retention and the
   weekly check), `reference/agents.md`, `reference/customers.md`, module boundaries; then `go generate`.

## Deviations

- A suggestion stores `old_text` (nullable) and `new_text` instead of a diff: applying replaces the one passage of the
  instructions that matches, or appends; a passage that is gone or occurs twice is `409 suggestion_stale`.
- Calibration also reads Auto-sent replies as labels: answered over is bad, left alone for 24 hours is good (without the
  good ones the bad share of Auto items would always read 100 %).
