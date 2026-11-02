# The Assistant, delivery C — bulk changes: implementation plan

> **For agentic workers:** implement this plan task by task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** a person asks "give every metering point in price area NO5 the product *Spot Bergen*"
or "set the payment terms of all customers tagged *Wholesale* to 30 days". The model calls
`propose_bulk` with a **selector** (an `auto` list operation and its filters), a **per-item
operation** (a `confirm` operation with the item's id bound into its path or body) and the
**values**. Nothing is written. The run pauses and the panel shows a **bulk card**: the change in
one sentence, how many items the selector found, a sample of 10 (current value → new value, as
B's card shows one item), how many the person may not change and why, and *Start* / *Cancel*.
*Start* runs a **canary**, the first 5 items for real, as the person, and the card shows each
outcome with *Continue* / *Stop*. *Continue* runs the rest as a **job** in chunks of 100 with
progress, a *Stop* that takes effect between chunks, a list of failed items with each refusal
and a CSV of it. The screen refreshes when the job ends and the model sums the outcome up in one
short message. It is a **preview, not a dry run**: handlers write directly and some have side
effects outside the transaction, so nothing can be rolled back, and nothing in the interface or
the documentation says otherwise.

**Architecture:**

- `internal/assistant/bulk.go` — the selector (read to the end through the dispatcher as the
  person, cap `maxBulkItems = 2000`, frozen), the plan (`propose_bulk`: the per-item operation is
  `confirm` through the gate, `Validate` passes for the first item with the binding applied, the
  binding names fields the items carry, one plan per run and never beside a single change), and
  the preview (count, sample of 10 with B's before-values read per item, the excluded and why).
  It stores B's pending action with `kind = 'bulk'` plus a job row and one row per frozen item.
- `bulk_run.go` — the runner: the canary in the *Start* request's own detached goroutine, the job
  in chunks of 100, a claim on the job row (`claimed_by`, `claimed_until`) renewed with every item
  in one statement that also counts it, stop and the Owner's narrowing re-read between chunks,
  the grant's remaining life checked between chunks, a 401 meaning the session ended, and the end
  of the job, which resumes the paused run so the model reads a compact summary.
- `bulk_http.go` — `continue`, `stop`, `GET` and `failures.csv` (`encoding/csv` plus the formula
  guard), and the bulk branch of `decideAction`.
- Migration `00105`: `assistant.bulk_jobs`, `assistant.bulk_job_items`,
  `pending_actions.kind`, `redact_preview` learns the bulk sample, and the claimed-action sweep
  leaves bulk actions alone (a job has its own claim). Both tables carry the conversation's
  `ON DELETE CASCADE`, so retention and the erasure sweep delete jobs with the conversations.

**Spec:** [`docs/superpowers/specs/2026-11-01-assistant-design.md`](../specs/2026-11-01-assistant-design.md), S13 (with S3, S12, S14, S16).
**Brief:** [`…/2026-11-01-assistant-briefs/c-bulk.md`](2026-11-01-assistant-briefs/c-bulk.md).
**Predecessors:** A #214, B #216 (`2026-11-02-assistant-writes.md`), T #218 (`2026-11-02-assistant-tours.md`).

## Plan readings

1. **Where a job can run.** `operations.Dispatcher` is built and bound only inside
   `module.Compose` (`compose.go`: `ops.Bind` after the router exists) and handed to the module
   named `assistant` alone. `module.Workers` (worker mode, and api mode with
   `WORKERS_IN_PROCESS=1`) never composes: the `module.Deps` it passes to a module's
   `Workers(deps)` has `Operations == nil`, so a worker-mode process has no bound dispatcher at
   all, and `server` mode runs no workers. **Decision: the job runs in the API process**, as a
   goroutine of the replica that served *Start* or *Continue* (the way a run does), holding a
   claim on its row. A test proves the worker deps carry no dispatcher
   (`internal/module/workers_test.go`) and the runner tests prove the claim.
2. **Resuming a lapsed claim** (a crash or a deploy) needs a dispatcher, so only an API replica
   can do it. It does, lazily, when it serves the person's `GET` of the job or of the conversation
   (a card that is open polls every 2 s), and — when the workers run in the API process — from the
   stale-runs worker, which finds the API's server through a registry keyed by the pool both were
   built over. A worker-mode process only expires. The claim is taken with one conditional
   `UPDATE`, so exactly one replica resumes; it continues from the next `pending` item.
3. **Grants.** `AssistantGrants.Issue` binds to the session of the request in its context, so it
   can be issued only inside *Start* or *Continue*. A job gets a one-hour grant (the maximum). A
   job with under 5 minutes of grant left pauses in `awaiting_continue` ("confirm to go on"), and
   the next *Continue* issues a fresh one. A signed-out session makes the router answer 401 for
   the grant, so the job stops at that item: `stopped`, `session_ended`.
4. **The run stays `awaiting_confirmation` for the whole job.** `ResumeRun` would make the run
   `running`, where the 10-minute stale-run sweep and the run ceiling (5 minutes) would kill it.
   *Start* therefore claims the action and starts the job without resuming the run; the person's
   next message is refused (`run_in_progress`) while the job lasts, as it is while a card waits.
   At the job's end the run is resumed once, as B resumes it after a decision, and the model
   answers in one short message (no tools).
5. **The pre-check.** Every item runs the same operation, so the operation-level pre-check
   (`Dispatcher.Effective` for the person, which applies the access rule through `List` and the
   Owner's levels through the gate) is all-or-nothing and is applied before the card is made:
   a person who may not change gets a refusal, not a card. Per item, the pre-check excludes an
   item that does not carry the bound id (`missing_id`). Row-level rules live in handlers and show
   only when the change runs; the card says so, and a refusal fails that one item.
6. **Pagination in the contracts.** 37 list operations take `page`/`pageSize` and answer
   `{data, pagination{page,pageSize,totalCount,totalPages,hasNextPage}}`; two take `cursor` and
   `limit` and answer `{data, nextCursor}` (customers' timeline, assistant's conversations); the
   rest take no paging parameter and answer everything (`{data}`, `{items}` or a bare array). The
   selector follows those three and refuses an operation that declares any other paging parameter
   (`offset`, `after`, `pageToken`, …) with a reason the model can act on.
7. **The shared job runner.** S13 says the runner becomes a shared platform helper; the brief says
   to extract one only if small and to leave the old copies alone. The assistant's job differs from
   the three it would replace: it is driven by a dispatcher, it is claimed in the API process and
   its counters are written per item with the claim, so a helper would carry a parameter for each
   difference. **Decision: the assistant keeps its own runner**, `module-boundaries.md` says so,
   and the existing runners are untouched.
8. **Tests that need a real list and a real write** compose real modules (`internal/integration`,
   beside B's); the runner rules (chunks, stop, narrowing, grant, sign-out, 429, claim, pool of
   one) run in the assistant package against the real database with a scripted dispatcher behind
   the small `bulkOps` interface, because a module test may not import another module.

## Tasks

- [ ] **1. The plan.** This file, committed alone, with no `Docs-Impact` trailer.
- [ ] **2. Migration, queries and contract.** `00105_assistant_bulk.sql`, sqlc queries,
  `openapi/assistant.yaml` (`continueAssistantBulkJob`, `stopAssistantBulkJob`,
  `getAssistantBulkJob`, `getAssistantBulkJobFailures`, the `kind` and `bulk` of a pending action,
  the bulk schemas), codegen, the specs copy and the TypeScript client.
- [ ] **3. The selector, the plan and the preview.** `propose_bulk`, `resolveSelector`, the card,
  `redact_preview`, the tool definition and the prompt.
- [ ] **4. The job.** *Start* with the canary, *Continue*, chunks, claim, stop, narrowing, grant,
  sign-out, the end and the run's continuation; the cancel of a previewed plan.
- [ ] **5. Endpoints and workers.** `GET`, `stop`, `failures.csv`; expiry of previews and of
  `awaiting_continue` jobs; resuming lapsed claims.
- [ ] **6. Go tests.** The assistant package (runner, claim, 404s, CSV, retention, erasure,
  pool of one), `internal/module` (the workers carry no dispatcher), `internal/integration` (the
  selector against real lists, freezing, the preview writes nothing, *Start* runs 5, an injection).
- [ ] **7. The frontend.** The bulk card (en, nb), polling, *Stop*, the CSV link, the badge,
  invalidation, tests, i18n parity.
- [ ] **8. Documentation.** In one commit: `user/assistant.md` and
  `user/workspace-administration.md` (en, nb), `reference/assistant.md`, `module-boundaries.md`,
  `ROADMAP.md`. No commit in the range carries a `Docs-Impact` trailer.
- [ ] **9. The gates.** `server:check`, `server:test`, `frontend:check`, `docs:check`,
  `gen:client:test`, `gen:client` with no diff, `go generate` with no diff, the host build and
  `go test ./cmd/vantigo/` (the boot test on a pool of four).
