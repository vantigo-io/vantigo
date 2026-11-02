# Brief D: a bulk item runs at most once

You are implementing decision **S17** of
`docs/superpowers/specs/2026-11-01-assistant-design.md`. Read S13 and S17 first, then
this brief. Delivery C (vantigo-io/vantigo#220) is on `main`. Model: Claude Sonnet 5.5.

## The outcome

When a replica dies while a bulk item's call is in flight, the replica that takes over
**never runs that item again**. It marks the item `unknown`: it may or may not have
been applied.

- The bulk card's failure list and the CSV show `unknown` items with the sentence
  "This change may or may not have been made; check it".
- Where the per-item operation declares an `Idempotency-Key` header parameter, every
  item sends a key derived from the job id and the item's position.
- Nothing else about C's behaviour changes.

## Read before you plan

- **The runner:** `apps/server/internal/assistant/bulk_run.go`, `bulk.go`,
  `bulk_csv.go`, `queries/bulk.sql`, `00105_assistant_bulk.sql`. Learn how an item is
  claimed, called, recorded and counted, and how a lapsed claim is taken over.
- **The tests:** `bulk_test.go`, `bulk_internal_test.go`,
  `internal/integration/assistant_bulk_test.go`.
- **The headers:** `internal/operations` (`CallRequest.Headers`; how a header
  parameter is declared and validated), and `openapi/communications.yaml` for the
  `Idempotency-Key` parameter.
- **The frontend:** `apps/assistant/frontend/src/components/bulk-card.tsx`.
- **The docs:** `docs/src/content/docs/{en,nb}/user/assistant.md`,
  `en/reference/assistant.md` ("Bulk changes").

## Build

1. **Migration `00106_assistant_bulk_once.sql`.** If `main` has taken 00106 when you
   start, use the next free number and say so.
   - `bulk_job_items.status` gains `in_flight` and `unknown`.
   - Add `in_flight_at`.
   - `bulk_jobs` gains an `unknown` counter.
2. **The runner, per item:**
   - Set `in_flight` (with the time) in its own committed statement **before**
     `Operations.Call`.
   - Then record the outcome as today, with the counter update in the same
     statement.
   - Keep the lease-connection rule: never hold a connection or a transaction across
     the call.
3. **Takeover.** When a replica takes a lapsed job over, first move every `in_flight`
   item to `unknown`, counting it as processed, then continue from the next `pending`
   item. A test kills the run between the `in_flight` write and the outcome write (a
   hook in the runner for tests is fine) and proves the item is not called again and
   ends `unknown`.
4. **The idempotency key.** When the per-item operation declares an `Idempotency-Key`
   header parameter, send `assistant-bulk-<job id>-<seq>`, built from the parsed UUID
   and integer, never from text. Test it with a communications operation that declares
   the header, or a test module if no realistic bulk target has one.
5. **The outcome summary and the job's `GET`** carry `unknown`.
6. **The card** (en and nb) lists `unknown` items first in the failure list, with the
   sentence. The **CSV** gets a `status` column if it lacks one.
7. **Documentation** (en and nb where bilingual):
   - **`user/assistant.md`:** what `unknown` means and what to do. Remove the sentence
     that said an item may run twice.
   - **`reference/assistant.md`:** the states, `in_flight_at`, takeover, the key.

## Do not

- Change how a job is previewed, started, continued or stopped.
- Retry an `unknown` item.
- Hold a connection across a dispatcher call.

## How to work

- **Branch:** `feat/assistant-bulk-once`, already created from `origin/main`. Its first
  commit carries this brief and the spec's S17 and S18. Work in the worktree you are
  given.
- **Setup:** `bun install --frozen-lockfile` and `mise run server:db` first.
- **Plan:** a short plan, `docs/superpowers/plans/2026-11-02-assistant-bulk-once.md`,
  committed alone.
- **Commits:** one per task, signed, Conventional Commits, each ending with
  `Co-Authored-By: Claude Sonnet 5.5 <noreply@anthropic.com>`.
  - **Never put a `Docs-Impact` trailer on any commit.** Never pass `--no-gpg-sign`.
- **The gates, all green, and rerun whole after any fix:**
  - `mise run server:check`, `server:test`, `frontend:check`, `docs:check`;
  - `bun run gen:client:test`, and `bun run gen:client` and `go generate ./...` with
    no diff;
  - the host build;
  - `go test ./cmd/vantigo/`.
- **CodeQL:** no strings built from untrusted text, no backtracking regular
  expressions, no allocation sizes summed from input.
- **The pull request:** `git push -u origin feat/assistant-bulk-once`, then
  `gh pr create --base main`:
  - title `fix(assistant): a bulk item runs at most once`;
  - the body ending with `🤖 Generated with [Claude Code](https://claude.com/claude-code)`.

  Do not poll CI.

## Report

The PR URL, the commits, the gates, the migration number, and any departures.
