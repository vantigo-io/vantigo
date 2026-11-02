# The Assistant, delivery D — a bulk item runs at most once: implementation plan

**Goal:** decision S17 of the [design spec](../specs/2026-11-01-assistant-design.md). When a
replica dies while a bulk item's call is in flight, the replica that takes the job over never
runs that item again: it marks it `unknown`. Nothing else about C's behaviour changes.

**Tasks** (one signed commit each):

1. **Migration `00106_assistant_bulk_once.sql` and the runner.** Items gain `in_flight` and
   `unknown`, `in_flight_at`; jobs gain an `unknown` counter. The runner writes `in_flight` in
   its own committed statement before `Operations.Call`, then records the outcome (from
   `in_flight`) with the counters in one statement. `ClaimBulkJob` moves every `in_flight` item
   of the job to `unknown` and counts it processed, in the same statement as the claim. A test
   hook kills the run between the two writes; the test proves the item is not called again.
2. **The idempotency key.** When the per-item operation declares an `Idempotency-Key` header
   parameter, each call sends `assistant-bulk-<job id>-<seq>`, built from the parsed UUID and
   the integer.
3. **The contract, the card and the CSV.** `unknown` in the item status and the job's counters
   (`openapi/assistant.yaml`, regenerated clients); the failure list shows `unknown` items
   first with the sentence, in English and Norwegian; the CSV gains a `status` column; the
   outcome summary carries `unknown`.
4. **Documentation (en and nb).** `user/assistant.md`: what `unknown` means and what to do; the
   sentence that said an item may run twice goes. `reference/assistant.md`: the states,
   `in_flight_at`, takeover and the key.

**Not changed:** how a job is previewed, started, continued or stopped; an `unknown` item is
never retried; no connection or transaction is held across a dispatcher call.
