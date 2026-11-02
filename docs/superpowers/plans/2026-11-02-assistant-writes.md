# The Assistant, delivery B — changes with confirmation: implementation plan

> **For agentic workers:** implement this plan task by task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** a person asks the assistant to change something. The model finds the operation and
calls it through `call_operation`; because the operation's effective mode is `confirm`
nothing is written. The run pauses in `awaiting_confirmation` and the panel (or the Assistant
app) shows a **confirmation card**: what will happen, the fields with current value → new
value where a read of the same record can say, *Confirm* and *Cancel*. *Confirm* runs the
change through the dispatcher as the person and the conversation goes on streaming; *Cancel*
tells the model and it answers in one line. A card can be decided minutes later, after a
reload or from another device, and expires after 24 hours. The Owner gets a daily token
budget per person and a usage view (counts, never content). Links in answers open only
Vantigo's own routes and `/docs`.

**Architecture:** `assistant.pending_actions` holds the request and the preview of one
proposed change, private to its person. `callOperation` on a `confirm` operation validates the
arguments, builds the preview (a read of the `GET` sibling through the dispatcher, as the
person), writes the pending action, answers the model "waiting for the person" and ends the
turn: `runs.state` becomes `awaiting_confirmation` and the grant is revoked. Two new
`session` endpoints, `POST /api/v1/assistant/pending-actions/{id}/confirm` and `…/cancel`,
answer `text/event-stream` through the same `streamResponse`: *confirm* atomically claims the
action (`pending` → `confirmed`, run `awaiting_confirmation` → `running`), issues a grant from
**that** request's session, and a resumed runner calls `Operations.Call` with
`Confirmed: true` and `If-Match` from the preview's ETag, replaces the waiting tool result with
the real one, records the outcome and the `actions` row, and continues the model loop. The
stale-runs worker expires undecided actions after 24 h on its single lease-less statement.
`assistant.usage_days` keeps counts per person and business day, so the budget and the Owner's
view survive a person deleting a conversation. The frontend gains the card, the `confirmation`
event, the badge state, the notification and the invalidation after success.

**Spec:** [`docs/superpowers/specs/2026-11-01-assistant-design.md`](../specs/2026-11-01-assistant-design.md), S12, S14 (the budget), S9 (the Owner's count view), S16 (*Read and change* takes effect).
**Brief:** [`…/2026-11-01-assistant-briefs/b-writes.md`](2026-11-01-assistant-briefs/b-writes.md).
**Predecessor:** [`2026-11-01-assistant-app.md`](2026-11-01-assistant-app.md) (delivery A, #214).

## Plan readings

1. **`callOperation`** (`assistant/tools.go`) refuses every non-`auto` operation as
   `changes_not_available`; the gate has already narrowed `mode` by the Owner's levels, so a
   `confirm` here is one the Owner's level allows. `Operations.Call` refuses a `confirm`
   without `Confirmed: true` *after* the gate, so a level narrowed while a card waits is
   refused as `operation_never` at confirm time (the "403 with a reason" case).
2. **The run loop** (`runs.go`): `execute()` loads the history from the stored messages, loops
   model turn → tool calls, and its deferred function revokes the grant, calls `finishRun`
   (`WHERE state = 'running'`) and emits `done`/`error`. A resumed run is the same row and the
   same code with a decision applied first. `ux_runs_one_running` and `ix_runs_running` are
   partial on `state = 'running'`; `runs.state` is `varchar(10)`, too narrow for
   `awaiting_confirmation`.
3. **`streamResponse`** (`sse.go`) satisfies only the send operation's generated response
   interface; confirm and cancel need their own `Visit…Response`, one writer shared.
4. **`CallResult`** carries no response headers, so a preview cannot read an ETag. It gains
   `Header` (the recorded response headers).
5. **Tests with a real write** need a real module, which the assistant package may not import
   (depguard, tests included): they live in `internal/integration` beside A's
   `assistant_run_test.go`, with `projects` (a `postProjects`, `putProjectsById`) and
   `customers`. The run, budget, usage and worker tests that need no write stay in the module.
6. **The business day:** `Deps.Calendar.Zone(ctx)` (a pool connection of its own, so read
   before any lease/transaction).
7. **Query keys:** the host's modules key TanStack queries by their own names; to be read in
   task 7 before the invalidation rule is chosen.
8. **Docs embedded in the binary** (`internal/assistant/docs`) are a `go generate` copy of the
   user and admin pages; a docs change in this delivery needs `go generate` or the freshness
   test fails.

## Tasks

- [ ] **1. The plan.** This file, committed alone, with no `Docs-Impact` trailer.
- [ ] **2. The dispatcher returns headers.** `operations.CallResult.Header`; test.
- [ ] **3. Migration, queries and contract.** Migration `00104` (`pending_actions`,
  `usage_days`, `settings.daily_token_budget`, `runs.state` width and the two indexes,
  `actions.confirmed_by`), sqlc queries, `openapi/assistant.yaml` (the two endpoints, `usage`,
  the part, run state, detail and settings changes, `429 budget_exhausted`), codegen, the
  specs copy and the TypeScript client.
- [ ] **4. Pending actions in the run.** Preview, pause, the `confirmation` event, confirm and
  cancel (resumed runner), the prompt, the stale-runs worker, stop, retention and erasure.
- [ ] **5. The budget and the usage view.** Counters, the check before each turn and at the
  start, the settings field, the usage endpoint.
- [ ] **6. Go tests.** In the module and in `internal/integration` (the list in the brief).
- [ ] **7. The frontend.** The card (en, nb), the stream client, reload, the badge and the
  notification, `invalidateAfterAssistantWrites`, `markdown.tsx`'s link rule, the budget field,
  the usage table, tests, i18n parity.
- [ ] **8. Documentation.** In one commit: `user/assistant.md` and
  `user/workspace-administration.md` (en, nb), `reference/assistant.md`, `ROADMAP.md`. No
  commit in the range carries a `Docs-Impact` trailer.
- [ ] **9. The gates.** `server:check`, `server:test`, `frontend:check`, `docs:check`,
  `gen:client:test`, `gen:client` with no diff, the host build and the boot check on a pool of
  four connections.
