# Knowledge and live updates — deliveries 0, K-A, K-B (and K-C, K-D later): implementation plan

The design is `docs/superpowers/specs/2026-11-07-knowledge-and-live-design.md` ("S"),
whose decisions are L*n* and K*n*.

## How it lands: one stack

The owner asked on 2026-11-07 for a **PR stack, so the work never waits for a merge**.

- **The tool.** The stack is managed with `gh stack`, always with its non-interactive
  flags (`view --json`, `submit --auto`, `add <branch>`).
- **Merging.** The owner merges with `gh stack merge <top PR> --yes`. After each lower
  merge, check that the upper PRs are still queued.
- **Each layer** is one PR whose diff is that delivery alone. Its body says what it sits
  on.

| # | Branch | Delivery | Migrations claimed (main at `2da71187`, latest `00131`; the Messenger stack holds `00132`–`00134`) |
| --- | --- | --- | --- |
| 1 | `claude/knowledge-base-app-d81641` | Spec and plan (docs only) | — |
| 2 | `claude/live-0-platform` | 0: `srv/live`, the slot, the shell hooks, the Communications inbox | — |
| 3 | `claude/knowledge-a-articles` | K-A: the module, articles, access, search, the Assistant | `00135_knowledge_baseline` |
| 4 | `claude/knowledge-b-editor` | K-B: the editor's comfort, files, links, Markdown | `00136_knowledge_files_links` |

K-C gets its own spec revision and a spike first (S §5). K-D's screens are separate small
PRs off `main` once 0 has landed.

**Migration numbers.** Right before each merge, `git ls-tree origin/main
apps/server/internal/db/migrations/ | tail`. If `main` took a claimed number, renumber
inside the owning layer (memory: migration-number-collisions).

**The docs layer carries no `Docs-Impact` trailer.** It changes only
`docs/superpowers/**`, which no page's `sources` names.

## How each delivery is run

The same routine as Orders and Messenger.

1. **The seam first, by one agent:**
   - the migration;
   - the OpenAPI contract with a `summary` on every operation;
   - `go generate` and sqlc;
   - the module skeleton and the registries.

   One commit, so the later lanes build on a compiling tree.
2. **Lanes.** Each lane owns disjoint files:
   - the server;
   - the shell or the module UI;
   - the host;
   - the other module's side (Communications in 0).
3. **Docs lane.**
   - The bilingual user and admin pages, the reference, and the contributing pages.
   - Then `go generate ./...` in `apps/server`, because the Assistant embeds the docs.
4. **Review.** An Opus code review of the server and the frontend, then fix what it
   finds.
5. **Gates:**
   - `mise run server:check`;
   - `mise run server:test`. On SQLSTATE 53300, rerun the failing packages with `-p 1`;
   - `mise run frontend:check` — read the log, not the exit line;
   - `mise run docs:check`, with check-coverage run against the previous layer as its
     base.
6. **Commits.**
   - Conventional Commits, signed. Never `--no-gpg-sign`, never filter-branch.
   - Order: `feat(<area>)` per area, then `docs(<area>)`, then `chore(assistant):
     regenerate the embedded documentation`.
7. **Then** `gh stack add <next branch>`, and the next delivery starts.
   - `gh stack submit --auto` pushes and opens the PRs.
   - Red CI is fixed in the layer that owns it, followed by `gh stack rebase --upstack`
     and `gh stack push`.
   - If the classifier blocks a force-push, the exact command goes to the owner.

## Layer 1 — the design documents

Commit S and this plan as `docs(knowledge): design and plan for live updates and the
Knowledge module`. Then `gh stack init` adopts this branch as the bottom.

## Layer 2 — delivery 0, live

**Seam (server):**

- **Dependency.** `go get github.com/coder/websocket@<latest tagged>`.
- **The package `srv/live`:**
  - `topic.go`: the grammar (L8), `Topic`, `ErrForbidden`, `ErrUnknown`;
  - `notify.go`: `Execer`, `Notify` (L6), and the payload codec;
  - `hub.go`: the registry, the subscriptions, fan-out with bounded queues (L10),
    presence (L13a);
  - `listener.go`: the dedicated connection (`pgxpool.ParseConfig` →
    `pgx.ConnectConfig`), LISTEN, backoff, `degraded` and `resync` (L7);
  - `presence.go`: the presence publisher's connection, batches and heartbeats, the
    names cache (L13a);
  - `conn.go`: the WebSocket read and write loops, limits, ping, the 60 s recheck
    (L11–L12);
  - `handler.go`: `GET /api/v1/live` — origin and `Sec-Fetch-Site`, session, rate
    limit, `LIVE_ENABLED` (L2);
  - `registry.go`: `Registry`, built by `Compose` from the topics and the directory (L9).
- **Identity.** `contracts.Access.SessionLive`, a non-sliding read, with its test (L11).
- **The module slot.** `module.Module.LiveTopics`, collected by `Compose` (prefix
  check, duplicates refused).
- **Deps.** `Deps.Live` carries the `*live.Registry`. `live.Notify` stays a free
  function.
- **Wiring:**
  - `server.New` takes the handler;
  - `cmd/vantigo` builds the hub in `api` and `server` modes, and closes it on the
    shutdown context before `srv.Shutdown`;
  - `config`: `LIVE_ENABLED` and `LIVE_DATABASE_URL`;
  - depguard: `internal/live` in the `platform` files;
  - `otelhttp`: the filter excludes `/api/v1/live`.
- **Telemetry** (L16).

**Lane — the shell and the host:**

- `packages/frontend-shell/src/live/`: `LiveProvider`, `useLiveTopic`, `usePresence` and
  `useLiveStatus`, with tests on a fake WebSocket.
- `apps/host/frontend`: mount the provider under the signed-in layout, keyed on the
  user id (L13), and set `ws: true` in `vite.config.ts`.

**Lane — Communications:**

- **Topics.** `LiveTopics`: `communications.inbox` and `communications.conversation`,
  whose `Authorize` reuses the conversation GET's visibility rule.
- **Notify points.** `live.Notify` at every write path in L18's table, starting from
  that table and checked against every write to the `conversations` and `messages`
  tables in `srv/communications/queries/*.sql`. They are listed in the reference page.
- **Tests.**
  - One test per path.
  - The test that walks the store write methods (L18).
- **Messenger.** If the Messenger stack's B or C has landed, rebase onto it first.
- **The UI:**
  - the inbox list and the conversation subscribe;
  - the fallback poll runs only while live is not `live`;
  - presence in the conversation, with the composer's `replying` state.

**Docs lane:** S §8, delivery 0's pages, then `go generate`.

**Verify in the browser.**

- **Setup.** `mise run dev`, with two browser profiles signed in as two people.
- **The inbox.** A mail is delivered to a test mailbox, or a conversation is created in
  the other profile, and the list updates without reload.
- **Presence.** It shows "Also here" and "is replying".
- **Restart.** When the server restarts, the socket reconnects with `resync`.
- **Status.** With `LIVE_ENABLED=false`, the status is `unavailable` and polling resumes.

## Layer 3 — delivery K-A, articles

**Seam:**

- **Migration** `00135_knowledge_baseline`:
  - the schema and S §7's tables;
  - the generated `search` column, capped and `coalesce`d (K12), and its GIN index;
  - no extension;
  - the down migration;
  - the schema in `db/schema_test.go`'s `moduleSchemas`, and the grants in
    `deploy/compose/README.md` and `admin/installation.md`.
- **Contract** `openapi/knowledge.yaml`:
  - **Tree:** `listKnowledgeTree`.
  - **Articles:** `createKnowledgeArticle`, `getKnowledgeArticle` (`never`),
    `getKnowledgeArticleText` (`auto`), `updateKnowledgeArticleContent`,
    `updateKnowledgeArticle` (title, icon, lock, `ai_hidden`), `moveKnowledgeArticle`.
  - **Access:** `getKnowledgeArticleAccess`, `updateKnowledgeArticleAccess`.
  - **History:** `listKnowledgeArticleVersions`, `getKnowledgeArticleVersion`,
    `restoreKnowledgeArticleVersion`.
  - **Trash:** `trashKnowledgeArticle`, `listKnowledgeTrash`,
    `restoreKnowledgeArticle`, `emptyKnowledgeTrash`.
  - **Favourites:** `listKnowledgeFavourites`, `addKnowledgeFavourite`,
    `removeKnowledgeFavourite`, `moveKnowledgeFavourite`.
  - **Search:** `searchKnowledge` (`auto`).
  - **Mode.** Every operation but `searchKnowledge` and `getKnowledgeArticleText` is
    marked `x-vantigo-assistant: never`. They go in the integration test's
    `markedByHand`, with a test that only those two are `auto` (K14).
- **Generated code.** `cfg-knowledge.yaml`, the `generate.go` lines, `sqlc.yaml`.
- **The module skeleton:** `module.go` with its permissions, `LiveTopics` and `Workers`
  (`knowledge.retention`).
- **Every registry in AGENTS.md's list,** and Orders' scaffold commit `adb00f48` as the
  checklist.
- **The package `srv/knowledge/doc`:** the allowlist, validation and text derivation,
  with its tests.

**Lane — the server:**

- **Access** (K4–K6):
  - the materialisation in `access.go`, under the tree's advisory lock (K5);
  - the random-tree reference test, and the concurrency tests;
  - `path` truncation (K12).
- **Articles and the tree:** fractional keys, the depth limit, cycle refusal.
- **Content saves:** versions and coalescing (K9).
- **Trash groups** (K10), **favourites**, the **lock**.
- **Search** (K12).
- **The Assistant's text operation** and `ai_hidden` (K14–K16).
- **The retention worker** (trash, versions, favourites, erasure; K17).
- **Live notify** on every write (K13).

**Lane — `@vantigo/knowledge-ui`:**

- **The sidebar tree.** Sections, drag to move and reorder (Mantine's tree or
  dnd-kit), favourites.
- **The article page:** the icon, the title, the editor (K7), autosave, the conflict
  dialog, the read-only banners (locked; Kari is editing).
- **The other screens:** the access dialog, the history drawer, the trash page, search.
- **The schema exporter** for K8's allowlist test.

**Lane — the host and the Assistant:**

- **The host:** routes, `apps.ts`, the dashboard tile, the spotlight source,
  `sessionClients`, admin labels.
- **The Assistant:** `appRoutes` and `APP_ROUTE_PREFIXES`, the prompt's Knowledge
  sentence (K15), and the `promptVersion` bump.

**Docs lane:** S §8, K-A's pages.

**Verify:**

- write and organise pages;
- share a page with a second user and see it under *Shared*;
- a private page is invisible to an administrator;
- search in Norwegian ("fakturaer" finds "faktura");
- the Assistant answers from an article with a link, and ignores an `ai_hidden` one;
- two browsers: *Kari is editing*, and the conflict dialog.

## Layer 4 — delivery K-B, the editor's comfort

**Seam:**

- **Migration** `00136_knowledge_files_links`.
- **Contract:** `uploadKnowledgeFile`, `getKnowledgeFile`, `exportKnowledgeArticle`,
  `importKnowledgeMarkdown`, `listKnowledgeBacklinks`.
- **The allowlist** grows by K18's nodes.

**Lanes:**

- **The server:** files through `storage` and `imagemeta`; link rows on save; mention
  notifications; Markdown export (a zip) and import (Obsidian callouts, wiki links).
- **The UI:** the slash menu, drag handle, unique ids, callouts, toggles, tables,
  lowlight, the table of contents, the image and file nodes, mentions, `[[` links,
  record chips, the backlinks footer, the export and import dialogs.
- **The licence list** goes in `reference/knowledge.md`.

**Docs lane:** the user page's editor section in both languages.

**Verify:**

- every block in the slash menu;
- drag a block;
- paste an image;
- export a subtree and import it back;
- an Obsidian vault imports with its callouts and links.

## Gotchas carried from earlier deliveries

- **Contract rules.**
  - Every OpenAPI operation needs a `summary`. The Assistant's mode is derived: reads
    are `auto`, writes `confirm`, files `never`.
  - K-A marks writes `never` explicitly (S K14). The integration test's `markedByHand`
    map pins hand-marked operations.
- **House status codes:**
  - `400` for field codes;
  - `409 version_conflict` for a stale version;
  - `404` for what the person may not see.
- **Docs edits.**
  - A docs edit needs `go generate ./...` in `apps/server`.
  - A `contracts/*.go` or module-slot change needs `module-boundaries.md`.
  - A prompt change needs `reference/assistant.md`.
- **File operations** (K-B's upload and export) must be listed in the integration test's
  `filesOrRedirects`.
- **Tests.**
  - A whole-tree `go test` can exhaust the shared Postgres, so run the packages alone.
  - The pre-commit hook checks the whole tree. Commit only when every lane's files are
    clean.
- **A fresh worktree** needs `bun install --frozen-lockfile`.
- **Agents** can write only inside this session's worktree. Parallel work uses Agent
  `isolation: "worktree"`.
