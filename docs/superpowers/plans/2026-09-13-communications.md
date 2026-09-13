# Communications Module Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Port the Communications module to Go, single-tenant and outbound-only, together with the object-storage port, the `Worker` port and the outbox that it needs.

**Architecture:** A module in the shape the customers, products and energy modules established — one Postgres schema, sqlc queries, contract-driven access through `module.Router` — plus three platform additions: `internal/storage` (an object-store port and an `fs` driver), a `Worker` port with a runner wired into `worker` mode and into `api` mode under `WORKERS_IN_PROCESS`, and the outbox with its three workers.

**Tech Stack:** Go 1.27, stdlib `net/http`, pgx v5, goose, sqlc, oapi-codegen, `wneessen/go-mail` through the existing `internal/mail` port, `internal/secrets` for per-channel credentials.

**Spec:** [docs/superpowers/specs/2026-09-13-communications-design.md](../specs/2026-09-13-communications-design.md)
**Behavioural authority:** [docs/superpowers/specs/2026-09-13-communications-inventory.md](../specs/2026-09-13-communications-inventory.md) — 19 sections; §19.1 consolidates the seven porting hazards and §11 tabulates where `docs/communications.md` disagrees with the .NET code.

## Global Constraints

- **Contract-driven access.** Every operation's `x-vantigo-access` is enforced by `module.Router`. Handlers never re-check what the router already checked. This module adds no sanctioned exception; the project has exactly two, both in other modules.
- **Two error vocabularies, both observable.** Every module error is `{error:{code,message,fields?}}` on `application/json`; the three stats endpoints alone emit RFC 7807 `ProblemDetails`. No `HttpValidationProblemDetails` exists anywhere, and every 404 is bare-bodied.
- **Per-endpoint ordering is deliberately inconsistent** and is ported endpoint by endpoint from inventory §2–§3, including the two handlers that split validation around the 404 in opposite directions and the two idempotency asymmetries.
- **Tenancy is dropped.** No `tenant_id` column, key or index survives. No primary key in this module contained it, so the drop touches indexes only.
- **Outbound only.** There is no inbound path. Reply answers 422 `recipients_missing` for conversations with no inbound message; `replyAllCc` is always empty. Port faithfully, document loudly, invent nothing.
- **Exact .NET message text** for every validation error, asserted byte-for-byte.
- **Time comes from `Deps.Clock`**, never `time.Now()`.
- **`depguard`**: `internal/communications/**`, tests included, may not import `internal/identity`, `internal/customers`, `internal/products` or `internal/energy`.
- **Ported-test census** in .NET test methods, a `[Theory]` counting once. 78 facts exist; 22 are dropped wholesale by scope. Each task states the count it ports; Task 14 states the total.
- **Run tests synchronously**, never backgrounded, under `taskset -c 0-3`. `go generate ./...` runs from `apps/server`, never a scoped subtree. After any `openapi/*.yaml` change, run `bun run gen:client` and commit the regenerated frontend schemas — CI fails otherwise.
- **Green before every commit**: `go test ./... -count=1`, `golangci-lint run`, `go generate ./...` with no drift.

---

### Task 1: The object-storage port and its `fs` driver

**Files:** Create `internal/storage/{storage.go,fs.go,fs_test.go,storage_test.go}`.

`ObjectStore` is `Put(ctx, key, r, contentType)`, `Get(ctx, key) (io.ReadCloser, error)`, `Exists(ctx, key) (bool, error)`, `Delete(ctx, key) error`; deleting a missing key succeeds. A scope wrapper prefixes `{scope}/` — scope names are canonical lowercase `[a-z0-9-]`, and callers pass only relative keys.

- [ ] **Step 1: Write the failing tests.** Key validation rejects traversal, URL-encoded, absolute, backslash, control-character and duplicate-prefix forms; a write lands atomically (temp file under the root, then replace); no API returns a physical path; a symlinked root is refused; a group/world-writable root is refused outside development and allowed with the development escape hatch only when the environment really is development; an unconfigured store fails closed with a "storage is not configured" error rather than a panic or a silent no-op.
- [ ] **Step 2: Run them and watch them fail.**
- [ ] **Step 3: Implement** the port, the scope wrapper and the `fs` driver.
- [ ] **Step 4: Green and lint.**
- [ ] **Step 5: Commit.** `feat(storage): add the object-store port and fs driver`

### Task 2: The `Worker` port and runner

**Files:** Create `internal/worker/{worker.go,runner.go,runner_test.go}`; modify `internal/module` for worker registration, `internal/config` for `WORKERS_IN_PROCESS`, and `cmd/vantigo/main.go`.

A `Worker` is `Run(ctx) error` plus a poll interval and a name. The runner starts every enabled module's workers as goroutines, logs each start and stop, propagates cancellation, and waits for them on shutdown within the existing shutdown timeout.

- [ ] **Step 1: Write the failing tests.** Workers run in `worker` mode; they run in `api` mode when `WORKERS_IN_PROCESS=1` and not otherwise; they never run in `server` mode; a worker returning an error is logged and does not take down its siblings; cancellation stops all of them; a disabled module contributes none.
- [ ] **Step 2: Run them and watch them fail.**
- [ ] **Step 3: Implement**, wiring `worker` mode to run workers rather than only serving health.
- [ ] **Step 4: Green and lint.**
- [ ] **Step 5: Commit.** `feat(server): add the worker port and runner`

### Task 3: Communications schema, skeleton and catalog

**Files:** Create `internal/db/migrations/00006_communications_baseline.sql`, `internal/communications/{sqlc.yaml,queries/,module.go,server.go,errors.go,unimplemented.go,harness_test.go,main_test.go}`.

19 tables from inventory §10 — the 21 entities minus `inbound_receipts` and `inbound_email_jobs`, with the six scan columns dropped from `message_attachments` and `attachment_uploads`. DDL defaults are added per spec D4. The conversations feed index is `(status DESC, last_activity_at DESC)` per D6.

- [ ] **Step 1: Write the failing schema test:** up, down, up; every table and index; the positional feed index verified from `pg_index` rather than from the DDL text; `message_events.delivery_id` is the only `RESTRICT` FK; no `tenant_id` anywhere.
- [ ] **Step 2: Run it and watch it fail.**
- [ ] **Step 3: Write** the migration, sqlc config, skeleton with all 28 operations stubbed and pending, and the five-permission catalog verbatim, `conversations-view` sensitive. Port the catalog test as an exact-set assertion.
- [ ] **Step 4: Green and lint**, with the coverage gate proven to fail in both directions.
- [ ] **Step 5: Commit.** `feat(communications): add the schema, skeleton and catalog`

### Task 4: Channels

**Operations:** `getCommunicationsChannels`, `postCommunicationsChannels`, `getCommunicationsChannelsById`, `putCommunicationsChannelsById`, `postCommunicationsChannelsByIdVerify`.

Per-channel SMTP credentials are sealed with `internal/secrets` under their own purpose and never returned. Verify uses the existing `internal/mail` destination guard. `UpdateChannel` validates the body **before** the 404 and its credentials **after** it (inventory §3).

- [ ] **Step 1: Port the tests** from inventory §18, including the split-validation ordering.
- [ ] **Step 2: Run them and watch them fail.**
- [ ] **Step 3: Implement.**
- [ ] **Step 4: Green**, shrink `pendingOperations`.
- [ ] **Step 5: Commit.** `feat(communications): channels and credentials`

### Task 5: Conversations, tags and read state

**Operations:** `getCommunicationsConversations`, `postCommunicationsConversations`, `getCommunicationsConversationsById`, `patchCommunicationsConversationsById`, `postCommunicationsConversationsByIdNotes`, `postCommunicationsConversationsByIdRead`, `getCommunicationsTags`, `postCommunicationsTags`, `putCommunicationsConversationsByIdTagsByTagId`, `deleteCommunicationsConversationsByIdTagsByTagId`.

PATCH checks `status` **before** the 404 and `customerId` **after** it. Permission pairings are inconsistent by design: create needs `conversations-reply` alone, tag add/remove need `conversations-manage` alone, notes and PATCH need manage+view.

- [ ] **Step 1: Port the tests**, pinning each ordering and each permission pairing.
- [ ] **Step 2: Run them and watch them fail.**
- [ ] **Step 3: Implement**, including `replyRecipients` returning `canReply=false` when no inbound message exists.
- [ ] **Step 4: Green**, shrink `pendingOperations`.
- [ ] **Step 5: Commit.** `feat(communications): conversations, tags and read state`

### Task 6: Attachment staging and download

**Operations:** `postCommunicationsConversationsByIdAttachments`, `getCommunicationsConversationsByConversationIdAttachmentsByAttachmentId`, `getCommunicationsAttachmentsByIdDownload`.

Uploads are created `clean` (spec D2). The `Idempotency-Key` replay is checked **before** existence, so a replayed key returns 200 for a deleted conversation. Downloads stream through the application endpoint and never expose a storage key. The 10 MiB limit comes from config (D3).

- [ ] **Step 1: Port the tests**, including the replay-before-existence asymmetry and a download that 404s without leaking storage details.
- [ ] **Step 2: Run them and watch them fail.**
- [ ] **Step 3: Implement** on `internal/storage`, with the staged-object reservation and its bounded expiry.
- [ ] **Step 4: Green**, shrink `pendingOperations`.
- [ ] **Step 5: Commit.** `feat(communications): attachment staging and download`

### Task 7: Reply and the composer

**Operations:** `postCommunicationsConversationsByIdReply`.

The inactive-channel 422 is checked **ahead** of the idempotency replay, so a replay against a deactivated channel returns 422 rather than the cached 200. Suppression normalisation uppercases (D7). Reply enqueues an outbox job; it does not send inline.

- [ ] **Step 1: Port the tests**, including the 422-before-replay ordering, `attachments_not_ready`, and `recipients_missing` for a conversation with no inbound message — the documented outbound-only consequence.
- [ ] **Step 2: Run them and watch them fail.**
- [ ] **Step 3: Implement.**
- [ ] **Step 4: Green**, shrink `pendingOperations`.
- [ ] **Step 5: Commit.** `feat(communications): reply and the composer`

### Task 8: Suppressions

**Operations:** `getCommunicationsSuppressions`, `postCommunicationsSuppressions`, `getCommunicationsSuppressionsById`, `deleteCommunicationsSuppressionsById`.

Normalisation **uppercases** and the uppercased value is stored and echoed everywhere it appears.

- [ ] **Step 1: Port the tests**, asserting the uppercased echo explicitly.
- [ ] **Step 2: Run them and watch them fail.**
- [ ] **Step 3: Implement.**
- [ ] **Step 4: Green**, shrink `pendingOperations`.
- [ ] **Step 5: Commit.** `feat(communications): suppressions`

### Task 9: Stats

**Operations:** `getCommunicationsStatsAttention`, `getCommunicationsStatsSummary`, `getCommunicationsStatsTimeseries`.

These three alone emit RFC 7807 `ProblemDetails`; everything else in the module uses `{error:{…}}`. Pin that difference.

- [ ] **Step 1: Port the tests**, including one asserting the `ProblemDetails` shape on a stats 400 and the `{error:{…}}` shape on a neighbouring endpoint.
- [ ] **Step 2: Run them and watch them fail.**
- [ ] **Step 3: Implement.**
- [ ] **Step 4: Green**, shrink `pendingOperations`.
- [ ] **Step 5: Commit.** `feat(communications): stats endpoints`

### Task 10: The AI draft and customer suggestion

**Operations:** `postCommunicationsConversationsByIdAiDraft`, `postCommunicationsConversationsByIdAiCustomerSuggestion`.

Available only when configured; otherwise `ai_unavailable`. Prompt limits are exact: at most 20 messages, 1500 characters each, 10000 characters of draft, context version `v1`, and the untrusted-context markers with their instruction that enclosed material is data only. `AiInteraction` rows record the digest, model, token counts and duration. No product catalog exists (spec D1), so `products: none` and `product_data=false`. No test may call a real provider.

- [ ] **Step 1: Port the tests** with a fake chat client, covering unavailable, not-found, a successful draft, and the customer suggestion.
- [ ] **Step 2: Run them and watch them fail.**
- [ ] **Step 3: Implement.**
- [ ] **Step 4: Green**, shrink `pendingOperations` to empty for communications.
- [ ] **Step 5: Commit.** `feat(communications): AI drafts and customer suggestions`

### Task 11: The outbox delivery worker

**Files:** Create `internal/communications/outbox.go` and its tests.

Claim by conditional update with a lease; `attempts` increments at claim, so terminality is `attempts >= max_attempts` post-increment. `delivery_attempted_at` is cleared on claim and committed in its own write immediately before the send. Deterministic `Message-Id` from the message id. Backoff `min(3600, 2^min(attempts,10))`. The possible-duplicate counter fires only on the lease-expired `processing` branch.

- [ ] **Step 1: Port the tests** plus a gated concurrency test — two workers, one job, exactly one delivery — with a gate that polls `pg_stat_activity` rather than sleeping.
- [ ] **Step 2: Run them and watch them fail.**
- [ ] **Step 3: Implement** on the `internal/mail` port.
- [ ] **Step 4: Green**, the gated test at `-count=10`.
- [ ] **Step 5: Commit.** `feat(communications): the outbox delivery worker`

### Task 12: The retention worker

**Files:** Create `internal/communications/retention.go` and its tests.

Takes the advisory lease — the only worker that does. Deletes `message_events` **before** `message_deliveries`. Also owns the staged-upload expiry re-homed from the deleted scanner (spec D3), enqueueing expired objects for deletion.

- [ ] **Step 1: Port the tests**, including a gated lease test and one proving the delete order, which fails if reversed.
- [ ] **Step 2: Run them and watch them fail.**
- [ ] **Step 3: Implement.**
- [ ] **Step 4: Green**, the gated test at `-count=10`.
- [ ] **Step 5: Commit.** `feat(communications): the retention worker`

### Task 13: The attachment-cleanup worker

**Files:** Create `internal/communications/cleanup.go` and its tests.

Durable object deletion through `attachment_cleanup_records`, retried on failure, using conditional-update claims with no advisory lock.

- [ ] **Step 1: Port the tests**, including a storage failure that leaves the record for retry rather than losing it.
- [ ] **Step 2: Run them and watch them fail.**
- [ ] **Step 3: Implement.**
- [ ] **Step 4: Green.**
- [ ] **Step 5: Commit.** `feat(communications): the attachment-cleanup worker`

### Task 14: Composition, metrics, smoke and docs

**Files:** Modify `cmd/vantigo/main.go`, `internal/config`, `scripts/smoke-image.sh`, `CONTRIBUTING.md`, `docs/communications.md`, `docs/superpowers/specs/2026-09-10-go-backend-port-design.md`; delete `internal/communications/unimplemented.go`.

- [ ] **Step 1: Compose.** Mount the module, register its three workers, add every config value from spec §7 to the redaction mirror.
- [ ] **Step 2: Close the gate.** Delete `unimplemented.go` and `pendingOperations`; `TestMain` becomes `os.Exit(contracttest.RequireCoverage(m, recorder))`.
- [ ] **Step 3: Metrics.** Emit the four metrics the inventory names, including `communications.outbox.possible_duplicate_sends`.
- [ ] **Step 4: Smoke.** One probe: `GET /api/v1/communications/conversations` answers 401 without a session.
- [ ] **Step 5: Docs.** Rewrite `docs/communications.md` to delete the Mailgun and scanning sections and correct the two places where it contradicts the code (the backoff cap and the advisory lease's reach); correct the parent design's §3.10 lease sentence; record the five deliberate divergences and the outbound-only limitation in `CONTRIBUTING.md`.
- [ ] **Step 6: Verify everything** — the full sweep, plus `bun run gen:client` if any contract file changed.
- [ ] **Step 7: Commit.** `feat(server): serve the communications module`

## Self-review against the spec

- **Spec coverage.** Storage port → Task 1. Worker port and runner → Task 2. Schema, skeleton, catalog → Task 3. The 28 operations → Tasks 4–10. Outbox → Task 11. Retention and the re-homed expiry → Task 12. Attachment cleanup → Task 13. Composition, metrics, smoke, docs and the divergence list → Task 14. D1 → Task 10; D2 → Tasks 3 and 6; D3 → Tasks 6 and 12; D4 and D6 → Task 3; D5 → Task 3; D7 → Tasks 7 and 8.
- **Test coverage.** Every task states the inventory §18 tests it ports; Task 14 states the total against the ~56 portable of 78.
- **Type consistency.** `storage.ObjectStore` is declared once in Task 1 and consumed in Tasks 6, 12 and 13. `worker.Worker` is declared once in Task 2 and implemented in Tasks 11, 12 and 13.
