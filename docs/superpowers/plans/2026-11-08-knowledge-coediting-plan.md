# Knowledge — delivery K-C (live co-editing) and K-D (the other live screens): implementation plan

The design is `docs/superpowers/specs/2026-11-07-knowledge-and-live-design.md` ("S"),
revision 5: K-C is §5 (K24–K36), K-D is §5a (K37–K43). This plan continues
`docs/superpowers/plans/2026-11-07-knowledge-and-live-plan.md` ("P1"), whose routine,
gates and gotchas apply unchanged; this file lists only what differs.

## How it lands: a stack on top of K-B

Delivery 0, K-A and K-B are the stack of P1. This plan adds four layers above it, or
above `main` once those have merged. The tool and the merging rules are P1's (`gh stack`
with its non-interactive flags; the owner merges with `gh stack merge <top PR> --yes`).

| # | Branch | Delivery | Migrations claimed (main at `00139`) |
| --- | --- | --- | --- |
| 1 | `claude/knowledge-c-design` | Spec revision 5 and this plan (docs only) | — |
| 2 | `claude/knowledge-c-server` | K-C: `srv/live` binary frames and `DocStore`, `knowledge.doc`, the update log, seed, compaction, the collaborative save | `00140_knowledge_coediting` |
| 3 | `claude/knowledge-c-client` | K-C: the shell's binary channel, the provider, the editor assembly, cursors, the page | — |
| 4 | `claude/live-d-screens` | K-D: customers and energy imports, billing runs, the calendar mailbox sync | — (none expected) |

**Why this order.** Layer 3 needs layer 2's contract and frames. Layer 4 touches four
other modules and `packages/frontend-shell` only for its hook, which delivery 0 has
shipped, so it could be cut from `main` instead; it is stacked so that the owner merges
once, and is moved to `main` with `git rebase --onto` if it must land first.

**Migration numbers.** Right before each merge, `git ls-tree origin/main
apps/server/internal/db/migrations/ | tail`. If `main` took `00140`, renumber inside
layer 2 (memory: migration-number-collisions).

**Docs layer rule.** Layer 1 changes only `docs/superpowers/**` and carries no
`Docs-Impact` trailer. Layers 2–4 change documented behaviour and carry their pages in
the same series.

## Layer 1 — the design documents

Commit S and this plan as `docs(knowledge): design K-C and K-D`. Then `gh stack add
claude/knowledge-c-server`.

## Layer 2 — K-C, the server

**Seam (one agent, one commit):**

- **Migration** `00140_knowledge_coediting` (K27): `knowledge.article_updates (article_id
  references articles on delete cascade, seq bigint, data bytea, created_by uuid null,
  created_at, primary key (article_id, seq))`, its down migration, and nothing else. The
  `knowledge` schema's grants are K-A's. The table in `db/schema_test.go`'s expectations
  if the schema test lists tables.
- **Contract** `openapi/knowledge.yaml`:
  - `updateKnowledgeArticleContent` gains `mode` (`single` default, `collab`);
    `expectedVersion` becomes required only for `single`. A `mode: collab` request without
    it is valid. The response is unchanged.
  - **New operation** `resetKnowledgeArticleLog` (`POST
    /knowledge/articles/{id}/collab/reset`, writers), `x-vantigo-assistant: never`,
    added to the integration test's `markedByHand`, with the existing test that only the
    two operations are `auto`.
  - `go generate` and sqlc for the new queries (`queries/article_updates.sql`):
    `ListUpdatesAfter`, `UpdateLogStats`, `AppendUpdate`, `SeedIfEmpty`, `CompactUpdates`,
    `GetUpdate`, `DeleteUpdates`, `NullUpdateAuthors`.
- **`srv/live` seams (compiling stubs):** `Topic.Doc DocStore` and the interface of S K26,
  `DocWrite`, `DocResult`, the frame codec (`binary.go`) with its constants, and the new
  control frames (`frames.go`). No behaviour yet.

**Lane A — `srv/live` (platform):**

- **`binary.go`:** the codec of K25: kind, topic length, topic, payload; per-kind size
  limits; a malformed frame closes with 1007; over a limit with 1009.
- **`conn.go`:** the read limit rises to 4 MiB + header; **text** frames keep the 4 KiB
  check in the hub; the 1003 close for binary goes (a binary frame for a topic without
  `Doc`, or without a subscription, is dropped and counted).
- **`doc.go`:** per-subscription state (`canWrite`, `lastSeq`, ack order), `doc_sync`,
  replay with the blocking enqueue (K28), the per-connection rate limit, ordered `ack` and
  `update_rejected`, the origin-hub skip, the per-doc fetch worker with coalescing into one
  `Replay(after)`, `doc_stale`, `doc_reset`, `compact_requested` at most once a minute per
  doc.
- **`awareness.go`:** local relay, per-connection newest-frame coalescing, one
  notification per doc per 250 ms on the presence publisher's connection, base64 under
  6 KiB, the 4 KiB raw cap (K30).
- **`recheck`:** also refreshes each doc subscription's `canWrite` (K26).
- **`notify.go`:** a payload field for the origin hub (`h` exists); `NotifyDoc`.
- **Metrics** (K36).
- **Tests (no database):** the codec, replay order and backpressure, never-echo,
  reader-drop and its count, the rate limit, ack/reject ordering, awareness coalescing
  and the size cap, a slow consumer on a doc topic, the recheck turning write off.

**Lane B — `srv/knowledge`:**

- **`doc_store.go`:** the module's `live.DocStore` over `article_updates` (K27): `Append`
  (row lock, `max(seq)+1`, limits, `log_full`, notify on the same transaction), `Seed`
  (`insert … where not exists` under the row lock), `Compact` (the refusals of K31, one
  transaction), `Replay`, `Fetch`, `CanWrite` (write access, not locked, not trashed).
- **`live.go`:** the `knowledge.doc` topic (`Authorize` as the article topic's; `Doc`
  set).
- **`articles.go`:**
  - `UpdateKnowledgeArticleContent` with `mode: "collab"` (K32): no version comparison,
    lock and allowlist unchanged, coalescing unchanged;
  - a `single` save and `RestoreKnowledgeArticleVersion` **delete the article's log in
    their transaction** and notify `doc_reset`;
  - `ResetKnowledgeArticleLog`.
- **`retention_worker.go`:** null `created_by` of erased users in `article_updates`
  (K17, K27).
- **Tests (against the test database), K27–K32:**
  - append: order, `seq` per article, stored limits, `log_full`;
  - **the seed race:** N concurrent seeds, exactly one row;
  - compaction: replaced range, a late one refused, an oversized one refused, a stranger
    refused;
  - cross-hub fan-out with two hubs: the origin hub skips, the other fetches; a removed
    row gives `doc_stale`;
  - `doc_reset` after a single save, after a restore, after the manual reset;
  - the collaborative save: no version check, the lock, the allowlist, coalescing;
  - a locked page or lost write access stops storing within the re-check;
  - erasure.

**Docs lane:** S §8's K-C rows:
- `reference/knowledge.md`: the doc topic, the frames, the limits, the log, the reset
  endpoint, the collaborative save, the packages and licences (K34);
- `contributing/platform-packages.md`: *Binary frames and documents* (K25, K26);
- `contributing/module-boundaries.md`: the `Topic.Doc` slot;
- `admin/installation.md` (en and nb): binary frames through proxies, the 4 MiB message
  size (K36);
- then `go generate ./...` in `apps/server`.

**Gates:** P1's. Layer 2 changes behaviour users cannot yet see, so its user page is
layer 3's.

## Layer 3 — K-C, the client

**Lane A — the shell (`packages/frontend-shell/src/live/`):**

- `LiveSocket` gains binary `send` and `onmessage`; `binaryType = "arraybuffer"`.
- `client.ts`: the codec (K25), `listenDoc`, the doc control frames, `resync` handling for
  doc topics. The shell does not import Yjs.
- **Tests (fake socket):** frames both ways, a shared subscription with `listen`, the
  reconnect resyncing every doc, an unknown binary frame ignored.

**Lane B — `apps/knowledge/frontend`:**

- **Catalog and packages:** `yjs`, `y-protocols`, `lib0`, `@tiptap/y-tiptap`,
  `@tiptap/extension-collaboration`, `@tiptap/extension-collaboration-caret`, pinned to
  the lockfile's resolved versions, licences recorded.
- **`editor/collab/provider.ts`:** the `CollabProvider` of K34: the document, the
  awareness, the ack queue and its resend, batching (100 ms, `Y.mergeUpdates`), seeding
  (K29: normalisation through a throwaway editor, `prosemirrorJSONToYDoc`), compaction
  (K31), `doc_reset`, apply errors (K35).
- **`editor/use-article-editor.ts` and `extensions.ts`:** `Collaboration` and
  `CollaborationCaret`, StarterKit's `undoRedo` off, the user's colour from a hash of
  the id over twelve accessible colours; the editor falls back to the K-A assembly when
  not collaborative.
- **`pages/article.tsx`:**
  - the mode of K33: collaborative while live and synced, K-A otherwise, switching only
    at a quiet moment;
  - the collaborative save (K32: 5 s idle, 30 s at most, jitter, skip on another's save,
    flush on leave, `mode: "collab"`);
  - the conflict dialog, the banner and the local draft hidden in collaborative mode;
  - restore in one transaction (K33);
  - the *paused*, *cannot be opened* and *replaced* messages (K35, K32), and the read-only
    editor for readers;
  - the title staying a field.
- **i18n:** the new strings, `nb` and `en`.
- **Tests (vitest):**
  - the provider on a fake socket: batching, acks, resend after a reconnect, a rejected
    seed, compaction on request, `doc_reset`, an update that throws;
  - **two clients through a fake server converge** after interleaved, duplicated and
    offline updates, and across a compaction;
  - the page: mode switching, saves, the messages.
- **The schema exporter and `schema.test.ts`:** the editor schema is unchanged by
  `Collaboration`, which adds no node; the test stays.

**Docs lane (en and nb):**
- `user/knowledge.md`: *Editing together*: cursors, undo, the lag of search and history
  behind the live text, locking, what *Restore* does while others are in the page, and
  the fall-back to single editing when the live channel is off. Replace the paragraph
  about *"Kari is editing"* and the conflict dialog with "when live is on / off".
- `reference/knowledge.md` already carries layer 2's; link it.
- Then `go generate ./...`.

**Verify in the browser** (two profiles, then three):

- type in the same paragraph together; cursors with names and colours;
- the first open of an old page does not double its text when two people open it at once;
- cut the connection mid-typing and restore it; text typed offline appears for the
  other;
- lock the page: the editor goes read-only;
- restore a version: the others see it as one change and can undo it;
- stop the server's listener (as in delivery 0's check): the page falls back to K-A mode
  and returns;
- `LIVE_ENABLED=false`: single editing as before.

## Layer 4 — K-D, the other live screens

Four small lanes, one commit series each, in separate worktrees (`isolation:
"worktree"`), because they touch disjoint modules. No migration, no contract change:
the topics are not operations.

**Each lane has the same parts**, from S K37:

1. `LiveTopics` on the module (`Kind`, `Authorize` through `live.HasPermission`) and the
   registry entry in the module's `module.go`.
2. `live.Notify` at each write path in the lane's table, on the transaction that
   writes; progress rate-limited to one a second per aggregate in the worker.
3. The tests: one per write path (a notification arrives on a hub subscribed to the
   topic after commit and not after rollback), `Authorize` for allowed, forbidden and
   unknown key.
4. The screen: `useLiveTopic` invalidates the query keys on an event; the
   `refetchInterval` returns `false` while `useLiveTopic` is `true`, else the former
   interval; a vitest per screen (poll off while live, on when not, an event
   invalidates, a `resync` refetches).
5. Docs: the module's reference page (topic, permission, notify points), the user page
   (the screen updates by itself; fallback refresh) in **en and nb**, and the platform
   page's topics table; then `go generate`.

| Lane | Topic | Server | Frontend | Pages |
| --- | --- | --- | --- | --- |
| **Customers imports** (K38) | `customers.imports` | `customers/imports.go`, `import_worker.go` | `apps/customers/frontend/src/api/imports.ts` and the import pages | `user/customers.md` (en, nb), `reference/customers.md` |
| **Energy imports** (K39) | `energy.imports` | `energy/imports.go`, `import_worker.go` | `apps/energy/frontend/src/api/imports.ts` and the import pages | `user/energy.md` (en, nb), `reference/energy.md` |
| **Billing runs** (K40) | `invoices.billing-runs` | `invoices/billing_runs.go`, `billing_worker.go` | `pages/billing-runs.tsx`, `pages/billing-run.tsx` | `user/invoices.md` (en, nb), `reference/invoices.md` |
| **Mailbox sync** (K41) | `calendar.mailbox-sync:<userId>` | `calendar/mailbox_sync.go`, `mailbox_worker.go` | `apps/calendar/frontend/src/api/mailbox-sync.ts` and the card | `user/calendar.md` (en, nb), `reference/calendar.md`, `admin/calendar.md` if the operator sees a change |

**A test keeps each list honest**, as L18's does: it walks the module's store methods that
write the screen's tables and requires each caller to notify (or be named in an
allow-list with its reason, e.g. `WriteImportRowOutcome`).

**Host notifications** are not in this layer (K37). `contributing/platform-packages.md`
says what a per-user topic will need.

**Verify:**

- start a customers import: the progress bar moves without a poll; the network tab
  shows no repeating request;
- do the same for an energy import and a billing run;
- connect a mailbox in another browser and see the card change;
- with the server's listener stopped, each screen polls again at its old interval.

## Gotchas specific to this plan

- **The advisory lock stays out of updates.** An append takes the article's row lock
  only (K27). A review that finds `pg_advisory_xact_lock(hashtext('knowledge.tree'))` in
  the update path has found a bug.
- **A notification carries text.** Binary awareness is base64 in the payload; the cap
  is 4 KiB raw (K30). The test builds the largest accepted frame and notifies it.
- **The read-limit change** affects every socket: the hub must still close a **text**
  frame over 4 KiB with 1009. Delivery 0's test for it stays and must pass.
- **`LIVE_ENABLED=false`** is the operator's switch back to single editing; every
  K-C path is reachable without a socket, so the page tests run with the provider absent.
- **A clock in tests.** The 5 s / 30 s / 1 min timers in the provider and the hub use
  the injected clock and fake timers, never sleeps.
- **P1's gotchas** (contract summaries, `markedByHand`, house status codes, `go generate`
  after any docs edit, the shared test Postgres and `-p 1`, `bun install
  --frozen-lockfile`, worktrees for parallel agents) apply to every layer.
