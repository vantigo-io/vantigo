# Brief C: bulk changes

You are implementing delivery **C** of the design
`docs/superpowers/specs/2026-11-01-assistant-design.md`. It carries decision **S13**.

Read the design in full first, then this brief. Deliveries **0, A, B and T** must be on
`main`: check for `apps/server/internal/assistant/pending.go` and for `show_tour` in
`tools.go`. If either is missing, stop and report. You work alone, from a cold start.
Model: Claude Sonnet 5.5.

## The outcome

A person asks: "give every metering point in price area NO5 the product *Spot Bergen*",
or "set the payment terms of all customers tagged *Wholesale* to 30 days".

- **The model calls `propose_bulk`** with:
  - a **selector**: an `auto` list operation and its filters;
  - a **per-item operation**: a `confirm` operation, with the item's id bound into its
    path or body;
  - the **values**.

  Nothing is written.
- **The run pauses** and the panel shows a **bulk card**:
  - the change in one sentence, in the person's language;
  - **how many** items the selector found;
  - **a sample of 10**, each with its current value → new value, the way B's card
    shows one item;
  - **how many the person may not change**, with the reason. This is pre-checked
    against the operation's access rule and the Owner's permission levels; row-level
    refusals can only show up when the change runs, and the card says so;
  - *Start* and *Cancel*.
- ***Start* runs a canary:** the first **5** items, for real, as the person. The card
  then shows each one's outcome and offers *Continue* or *Stop*.
- ***Continue* runs the rest as a job,** in chunks of 100:
  - the card shows progress (processed, succeeded, failed, skipped) and a *Stop*
    button;
  - stopping takes effect between chunks;
  - at the end, the card lists the **failed items**, each with its refusal (status and
    the problem's title), and offers the list as a CSV download.
- **The screen refreshes** when the job ends, using B's invalidation.
- **The model reads the job's outcome** when the job ends, if the person is still in
  the conversation, and sums it up in one short message.

**This is a *preview*, not a dry run.** Handlers write directly, and some have side
effects outside the transaction, so nothing can be rolled back generically. The card
never says "dry run" or "test", and the documentation explains why.

## Read before you plan

- `AGENTS.md`, `CONTRIBUTING.md`, `module-boundaries.md` (rules 18 and 19, and the
  lock discipline), `platform-packages.md` (`operations`, `worker`).
- The spec, all of it: S3 (grants are bound to a session), S12, S13, S14 and S16.
- **The plans and pull requests of A, B and T:**
  - A: `docs/superpowers/plans/2026-11-01-assistant-app.md`, vantigo-io/vantigo#214;
  - B: `2026-11-02-assistant-writes.md`, #216. Read **"What C must know"** in B's
    report: the pending action's shape, `toolbox.propose`, `runner.createPending`,
    `PauseRun`, `decideAction`, `runner.decide` and `perform`, `ConfirmationCard`,
    `useConversation.confirm` and `cancel`, and `invalidateAfterAssistantWrites`;
  - T: its plan and PR.
- **`apps/server/internal/operations/`:**
  - `Call`, `Validate`, `Describe`, and how the dispatcher is **bound** (only after the
    API router is composed: `ErrNotReady` otherwise);
  - `apps/server/cmd/vantigo/main.go`: the `api`, `server` and `worker` process modes
    and `WORKERS_IN_PROCESS`.

  **Find out whether a worker in `worker` mode has a bound dispatcher.** If it does
  not, the job cannot run in a plain worker there. See step 5.
- **The existing long jobs, for the state-row shape:** `energy.imports`
  (`energy/import_worker.go`, `00049_energy_imports.sql`), `customers/import_worker.go`,
  and invoices' billing runs.
  - These are the pattern for counters, `stop_requested` and chunking.
  - **The lease-connection rule** (main's `5b3b76d4` and the fixes before it): a
    worker never holds one pool connection while waiting for another. There is a boot
    test on a pool of four in `cmd/vantigo`.
- **Pagination in the contracts.** It is not uniform: some lists take a cursor
  (`nextCursor`), some take a page or a limit and offset, and some return everything.
  Read the list operations the realistic selectors would use, starting with energy's
  metering points, customers, projects and invoices.

## Build

1. **The selector** (`internal/assistant`):
   - **Accepted only when:**
     - the operation is `auto` for the person through the gate;
     - it is a `GET`, or a read-only `POST` marked `auto`;
     - its response has an array of items whose objects carry the id the per-item
       operation needs.
   - **Paging:** support the conventions the contracts actually use, cursor and
     page/limit. Refuse a list whose paging you cannot follow, with a reason the model
     can act on.
   - **Read to the end,** with a cap of **2 000 items**. Over the cap, refuse with the
     count, so the model can narrow the filter. Make the cap a constant in the code
     and document it.
   - **Freeze the items.** The selector is resolved once, at preview time, and the ids
     are stored. The job acts on exactly what the person saw counted, never on a
     re-run of the selector.
2. **The plan and its validation:**
   - **The tool:** `propose_bulk({selector: {operationId, query}, operation:
     {operationId, pathParams, body}, bind: {<param or body field>: <item field>},
     explain})`.
   - **Check:**
     - the per-item operation is `confirm` (not `auto` or `never`) through the gate;
     - `Validate` passes for the first item with the binding applied;
     - the binding names fields the items actually have;
     - **one bulk plan per run**, and never while a single change (B) is waiting.
   - **Store it** as B's pending action with a `kind` of `bulk`, plus a new table
     (step 4). Reuse B's flow: `propose` → `createPending` → `PauseRun` → decide.
3. **The preview** (S13):
   - the count;
   - the sample: the first 10 items, with B's before-value read per item;
   - the excluded count and reasons, from the operation's rule against the person's
     permissions and the Owner's levels;
   - a note that row-level refusals show only when the change runs.

   The preview **never writes**. Test that at the database.
4. **Tables** (next free migration number):
   - `assistant.bulk_jobs`:
     - `id`, `pending_action_id`, `run_id`, `conversation_id`, `user_id`;
     - `operation_id`, `template` jsonb (path params, body and binding; the values are
       redacted after the end, as B redacts);
     - `state`: `previewed` | `canary` | `awaiting_continue` | `running` | `stopping`
       | `done` | `stopped` | `failed` | `expired`;
     - `total`, `processed`, `succeeded`, `failed`, `skipped`;
     - `stop_requested`;
     - `grant_id`, `grant_expires_at`;
     - `created_at`, `started_at`, `ended_at`, `error_code`.
   - `assistant.bulk_job_items`:
     - `job_id`, `seq`, `item` jsonb (the bound ids and the label for the list);
     - `status`: `pending` | `succeeded` | `failed` | `skipped`;
     - `http_status`, `problem_code`, `problem_title`, `done_at`;
     - primary key `(job_id, seq)`.
   - **Both are private** like every other assistant table: filtered by `user_id`, 404
     to anyone else, deleted with the conversation, and covered by retention and
     erasure.
5. **Running the job:**
   - **Every item goes through the dispatcher as the person**, with `Confirmed: true`.
     That needs an assistant grant, and a grant needs the person's session:
     - **at *Start* and *Continue*** (session requests), issue a grant for the job
       with the maximum TTL (1 h);
     - **when the grant has under 5 minutes left,** the job pauses in
       `awaiting_continue` with the reason "confirm to go on". The person's next
       *Continue* issues a fresh grant;
     - **when the person signs out,** the grant fails, so the job stops at the next
       item (`stopped`, `session_ended`). That is S3's rule, and the documentation
       says it.
   - **Where it runs.** Run the job **where the dispatcher is bound**:
     - if a `worker`-mode process has no bound dispatcher, run it in the API process,
       with a claim on the job row (`claimed_by`, `claimed_until`, renewed per chunk),
       so exactly one API replica runs a job;
     - a job whose claim lapsed (a crash or a deploy) is resumed by the next replica
       that sees it, from its next `pending` item.

     Prove this in a test, and say which you chose and why.
   - **The canary** runs the first 5 items in the *Start* request's own stream, then
     the job moves to `awaiting_continue`.
   - **Chunks of 100.** Between chunks:
     - re-read `stop_requested`;
     - re-check the gate (an Owner narrowing mid-job stops it: `stopped`, `narrowed`);
     - renew the claim;
     - write the counters.
   - **No connection held while waiting for another.** Never hold a transaction or an
     advisory lock across `Operations.Call`. Add a test for the runner on a pool of
     one.
   - **Per item:**
     - 2xx is `succeeded`;
     - 4xx and 5xx are `failed`, with the status and the problem's `code` and `title`
       only, never the body;
     - an item the pre-check excluded is `skipped`;
     - a 429 (rate limit) waits the `Retry-After` and retries once.
   - **The run continues when the job ends:** feed the model a compact summary (the
     counts and the first 5 failures), and stream its short answer if a client is
     attached. Otherwise persist it.
6. **Endpoints** (`session` access, `x-vantigo-assistant: never`):
   - `POST …/pending-actions/{id}/confirm` with a bulk action means *Start*.
   - `POST …/bulk-jobs/{id}/continue`, `POST …/bulk-jobs/{id}/stop`.
   - `GET …/bulk-jobs/{id}` (state, counters, the last 50 failures).
   - `GET …/bulk-jobs/{id}/failures.csv`. Build it with `encoding/csv` and guard
     against formula injection the way `fileimport`/the exports do.
7. **Workers:**
   - the stale-runs worker expires bulk previews and `awaiting_continue` jobs after 24
     h;
   - it resumes lapsed claims, if you chose the API runner;
   - both keep the lease-connection rule.
8. **The frontend:**
   - **The bulk card** (en and nb), built from B's `ConfirmationCard` parts:
     - preview: the count, the sample table, excluded, *Start*/*Cancel*;
     - canary results with *Continue*/*Stop*;
     - progress with a bar, the counters and *Stop*;
     - the end: the summary, the failed list (first 50), *Download failed items
       (CSV)*.
   - Progress polls `GET …/bulk-jobs/{id}` every 2 s while the job runs.
   - **The badge:** *working* while a job runs, *waiting for you* at
     `awaiting_continue`.
   - **Invalidation** when the job ends.
   - **The Assistant app's conversation view** shows the same card.
9. **The prompt:**
   - use `propose_bulk` for "all", "every" and "each" requests over more than a few
     items, never a loop of single changes;
   - say the count before proposing;
   - never claim a job is done before its outcome arrives.
10. **Documentation** (en and nb where bilingual):
    - **`user/assistant.md`:** bulk changes, covering:
      - preview, canary and run;
      - why it is a preview and not a dry run;
      - the 2 000-item cap;
      - stopping;
      - failed items and the CSV;
      - why a long job asks to *Continue*, and that signing out stops it.
    - **`user/workspace-administration.md`:** that the Owner's levels apply to every
      item, and that narrowing a level stops a running job.
    - **`reference/assistant.md`:**
      - the tables, states, endpoints and codes;
      - the selector rules and paging;
      - freezing;
      - grants and claims;
      - the runner's location and why;
      - redaction.
    - **`module-boundaries.md`:** if you add a platform job helper, document it; if
      you do not, say in the plan why the assistant keeps its own runner.
    - **`ROADMAP.md`:** C done; the Assistant's design complete.
11. **Tests:**
    - **Go:**
      - the selector's paging conventions, the cap, a refused list;
      - freezing: an item added after the preview is not touched;
      - the preview writes nothing;
      - *Start* runs exactly 5;
      - nothing runs past the canary without *Continue*;
      - a row-level refusal fails one item and the rest go on;
      - stop between chunks;
      - an Owner narrowing mid-job;
      - grant renewal pausing the job;
      - sign-out stopping it;
      - a lapsed claim resumed once, never twice;
      - another person's job is 404;
      - the CSV, formula injection included;
      - the runner on a pool of one;
      - retention and erasure;
      - the boot test still passing.
    - **An injection test:** a tool result tells the model to bulk-delete. The bulk
      card appears and nothing is written without *Start*. Assert at the database.
    - **Frontend:** the card's five states, polling stopping at the end, *Stop*, the
      CSV link, the badge, and i18n parity.

## Do not

- Run any item without the person's *Start*, or run past the canary without
  *Continue*.
- Re-run the selector after the preview.
- Call it a dry run.
- Hold a connection or a lock across a dispatcher call.
- Store response bodies.
- Exceed 2 000 items.
- Let anyone but the person see or control their job.
- Refactor the existing import or billing runners. Extract a shared helper only if
  the assistant needs it and it is small, and leave the old copies alone.

## How to work

- **Branch:** `feat/assistant-bulk`, cut from `origin/main` after T merged, in the
  worktree the session that started you names.
- **Setup:** `bun install --frozen-lockfile` and `mise run server:db` first.
- **Plan first:** `docs/superpowers/plans/2026-11-02-assistant-bulk.md`, in the house
  shape, with the docs pages as their own task. Commit it alone.
- **Commits:** one per task, Conventional Commits, each ending with
  `Co-Authored-By: Claude Sonnet 5.5 <noreply@anthropic.com>`.
  - Commits are signed; never pass `--no-gpg-sign`.
  - **Never put a `Docs-Impact` trailer on any commit.**
- **The gates, all green before you push:**
  - `mise run server:check`, `server:test`, `frontend:check`, `docs:check`;
  - `bun run gen:client:test`, and `bun run gen:client` with no diff;
  - the host build;
  - `go test ./cmd/vantigo/`.

  If you fix a failure, rerun the whole gate. Read the logs.
- **CodeQL runs on the PR.** Never build header values, CSV, HTML or quoted strings by
  concatenating untrusted text. Use `encoding/csv` and typed values.
- **The pull request:** `git push -u origin feat/assistant-bulk`, then
  `gh pr create --base main`:
  - title `feat: the Assistant makes bulk changes — preview, canary, run`;
  - the body ending with `🤖 Generated with [Claude Code](https://claude.com/claude-code)`.

  Do not poll CI.

## Report

- The PR URL and the commits.
- The gates.
- The plan and the migration.
- Where the job runs and why.
- The paging conventions supported.
- Departures from the spec.
- Anything left for a later phase.
