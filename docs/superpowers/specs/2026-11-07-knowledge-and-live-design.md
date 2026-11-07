# Knowledge and live updates — the `live` platform package and the Knowledge module (phase 1)

Revision 3, 2026-11-07 (after an Opus critic review and the K-A code review; §14 lists what changed). Drawn from the owner's conversation of 2026-11-07, which agreed:

- a knowledge base in the manner of Odoo Knowledge;
- a TipTap editor with Notion-like comfort;
- the Assistant reading the articles the person may read;
- a general real-time channel built first, so that live co-editing and other live screens can use it.

Decisions are numbered **L*n*** (the platform package) and **K*n*** (the module). The
implementation plan is `docs/superpowers/plans/2026-11-07-knowledge-and-live-plan.md`.

Abbreviations:

- `srv/` stands for `apps/server/internal/`;
- `mig/` stands for `srv/db/migrations/`;
- "MB" stands for `docs/src/content/docs/en/contributing/module-boundaries.md`.

## 1. Purpose

**Live** (`srv/live`, a platform package) lets the browser hear that something changed
without polling:

- **Ordinary screens.** A list or a detail page refreshes the moment a row it shows
  changes, on whichever replica or worker process changed it.
- **Presence.** A page shows who else is looking at it and who is editing ("Kari is
  replying").
- **Documents, later.** Delivery K-C carries Yjs co-editing over the same channel.

**Knowledge** (`knowledge`; Norwegian **Kunnskap**; package `@vantigo/knowledge-ui`) is a
shared space for what a company knows that has no other home in Vantigo: routines,
how-tos, onboarding, meeting notes, policies.

- **Pages.** Articles are pages in a tree, written in a block editor.
- **Access.** Each article is shared with the whole workspace, with chosen people, or with
  nobody but its owner.
- **Search.** Articles are searchable, and searchable by the Assistant on the person's behalf.

**Five deliveries, in this order:**

| Delivery | What it brings | Depends on |
| --- | --- | --- |
| **0** | **Live.** `srv/live` (the WebSocket endpoint, topics, `live.Notify`, the listener, presence), the `LiveTopics` module slot, the shell's `LiveProvider`, `useLiveTopic` and `usePresence`, the operator documentation, and the **Communications inbox** as the first live screen | — |
| **K-A** | **Articles.** The module, the tree, access (workspace, members, private), the editor with the basic blocks, autosave with versions and history, the lock, the trash, favourites, full-text search, the Assistant's read operations, presence on an article | 0 |
| **K-B** | **The editor's comfort.** Slash menu, drag handles, callouts, toggles, tables, code highlighting, images and files, `@mentions`, `[[links]]` with backlinks, links to Vantigo records, the table of contents, Markdown export and import | K-A |
| **K-C** | **Live co-editing.** Yjs through TipTap's Collaboration extensions, the update log, compaction, cursors | K-B, 0 |
| **K-D** | **The other live screens**, one at a time: imports, billing runs, calendar mailbox sync, the host's notifications | 0 |

K-D does not wait for Knowledge. Each of its screens is a small PR of its own, made
whenever convenient.

**Not in this phase:**

- spaces per team or department;
- groups as members;
- item views (kanban, cards, calendar) and properties;
- comments in the text;
- templates;
- publishing an article to the web;
- embeddings and semantic search;
- the Assistant writing articles.

§13 lists what each waits for.

## 2. Decisions — delivery 0, live

### The package and its place

- **L1 — `srv/live` is a platform package.**
  - It imports no business module. It is listed in depguard's `platform` set in
    `apps/server/.golangci.yml`, and described in
    `contributing/platform-packages.md` beside the outbox.
  - A process that serves HTTP (`api`, `server`) builds one `live.Hub`. A `worker`-only
    process builds none but can still call `live.Notify` (L6). That is the reason the fan-out goes through
    Postgres and not through memory: inbound mail and Messenger messages are written by
    workers, which may run in another process.
- **L2 — The endpoint is `GET /api/v1/live`, mounted by `server.New` outside `Compose`.**
  The composed router refuses patterns that are not contract operations
  (`module/router.go:230`). The socket is a platform concern, not a module's contract.
  - **Not in the API reference.** It is not an OpenAPI operation. It therefore never
    reaches the API reference or the Assistant's operations, and is documented in
    `contributing/platform-packages.md`.
  - **Sign-in.** The handler calls `Access.Check(r, session)` itself and answers
    `Access.Reject` on failure, before upgrading.
  - **Origin.** It accepts the handshake only when `Sec-Fetch-Site` is `same-origin` or
    the `Origin` equals the application origin, the rule `CrossOriginProtection` applies
    (`server.go:46`). A missing `Origin` is refused with `403`.
    - Why it checks itself: `CrossOriginProtection` skips `GET`.
    - Why both forms: in development the browser's origin is Vite's.
    - The session cookie's `SameSite=Strict` already keeps it off cross-site handshakes;
      this check is the second layer.
  - **Where it is matched.** The route is matched inside `server.New`'s `/api` branch.
    It is excluded from the `otelhttp` filter (`telemetry/http.go:16`), so a socket never
    becomes an hours-long span.
  - **The connection's lifetime.** The upgrade handler's goroutine owns the socket until
    it closes, because the request's context ends when `ServeHTTP` returns.
  - **Rate.** Opening a connection is rate-limited per user through `Deps.Limiter` (key
    `live.connect`: 30 a minute).
  - **Switch.** `LIVE_ENABLED` (default `true`) turns the endpoint off. It then answers
    `404`, and every screen keeps its fallback polling (L13).
- **L3 — The WebSocket library is `github.com/coder/websocket`.** It is context-aware,
  has no dependencies, is maintained, and reaches the connection through
  `http.ResponseController`, as `assistant/sse.go` already does. The version is pinned in
  `go.mod` at implementation.

### The wire

- **L4 — JSON text frames, versioned by the path's query `?v=1`.** Binary frames are
  reserved for K-C's document topics.
  - **Client → server:**
    - `{"type":"sub","topic":"…"}` and `{"type":"unsub","topic":"…"}`;
    - `{"type":"presence","topic":"…","state":"viewing"|"editing"|"replying"|null}`
      (`null` leaves);
    - `{"type":"ping"}`.
  - **Server → client:**
    - `{"type":"ok","topic":"…"}` after a sub;
    - `{"type":"denied","topic":"…","reason":"forbidden"|"unknown_topic"|"limit"}`;
    - `{"type":"event","topic":"…","kind":"…","id":"…"}`. Here `id` is optional and is
      only ever an identifier;
    - `{"type":"presence","topic":"…","members":[{"userId","name","state","since"}]}`, the
      whole set each time;
    - `{"type":"resync"}`: you may have missed events, refetch what you show;
    - `{"type":"pong"}`.
  - **Frame size.** A client frame over 4 KiB closes the connection with 1009.
- **L5 — Events carry identifiers only, never data.** A screen that hears an event
  refetches through the ordinary API, which checks the person's permission as it always
  does.
  - **Permissions.** The socket never carries what the person could not fetch.
  - **Missed events.** A missed event is harmless once a `resync` follows.
  - **Payload size.** NOTIFY's payload limit (8000 bytes) is never near.

### Publishing

- **L6 — `live.Notify(ctx, q live.Execer, topic, kind, id string) error`.**
  - **How.** It runs `SELECT pg_notify('vantigo_live', $1)` on `q`, with the payload
    `{"t":topic,"k":kind,"id":id}`. `Execer` is the one-method interface that `pgx.Tx`,
    `*pgxpool.Pool` and a sqlc `DBTX` satisfy.
  - **On a transaction.** Postgres delivers a notification **only when the transaction
    commits**, and drops it on rollback. A module therefore notifies on the same `tx` that
    writes the row, the way it calls `events.Publish`. Notifying on the pool is for a
    write that is not in a transaction (Communications' note insert today).
  - **Validation.** `Notify` checks the topic against the topic grammar (L8) and refuses a
    payload over 7 KiB. The id is free text, so a long id must not slip a large payload
    through.
  - **What a notify costs.** Postgres takes one cluster-wide lock at commit for every
    transaction that notified, held through the WAL flush. Notifying commits are
    therefore serialised across the cluster. Business code notifies once per changed
    aggregate per transaction, never inside a loop over many rows (a bulk change sends
    one event without an id). The shared Postgres of vantigo-infra was considered: the
    rate expected (UI-scale writes) is far below what this lock limits.
  - **Ordering.** Notifications reach the listeners in commit order, and duplicates within
    one transaction are folded by Postgres. A module may notify freely. A burst is
    debounced in the browser (L13), not in the server.

### Listening and fan-out

- **L7 — One listener per hub, on its own connection, not from the pool.**
  - **The connection.** `live.Hub` opens a dedicated connection: `pgxpool.ParseConfig`
    of `DATABASE_URL`, then `pgx.ConnectConfig(ctx, cfg.ConnConfig)`. A plain
    `pgx.Connect` would pass the pool's `pool_max_conns`, which the installation guide
    tells operators to set, to the server as a runtime parameter, and fail. The hub then
    runs `LISTEN vantigo_live` and loops on `WaitForNotification`. A held
    pool connection would starve the pool: the lease workers' pool-of-one tests and the
    deadlock of PR #194 are the reason this is not `Pool.Acquire`.
  - **Sizing.** The operator's connection sizing grows by **two** per HTTP replica
    (`admin/installation.md`, database connections): the listener, and the presence
    publisher of L13a.
  - **Poolers.** LISTEN does not work through a transaction-mode pooler (PgBouncer in
    transaction mode, a managed provider's built-in pooler). `LIVE_DATABASE_URL`,
    optional, points the two live connections at the direct port. When it is unset they
    use `DATABASE_URL`.
  - **Reconnecting.** When the connection is lost, the hub reconnects with backoff
    (1 s doubling to 30 s, with jitter). After every reconnect it sends `resync` to every
    socket, because it cannot know what it missed.
  - **Degraded.** While the listener is down, the hub sends `{"type":"degraded"}` to
    every socket and to new ones. The provider treats that as not `live`, so fallback
    polling resumes (L13). The return of the listener sends `resync`.
  - **Health.** The listener's state and `pg_notification_queue_usage()` go in the health
    detail on the management listener.
- **L8 — Topics are `<module>.<kind>` or `<module>.<kind>:<key>`.** The grammar is
  `^[a-z]+\.[a-z-]+(:[A-Za-z0-9_-]{1,64})?$`. Examples: `communications.inbox`,
  `communications.conversation:0b9e…` (conversation ids are UUIDs), `knowledge.article:5f1c…`, `knowledge.tree`.
- **L9 — A module registers the topics it serves through a new slot,
  `Module.LiveTopics func(Deps) []live.Topic`.**
  - **The type.** `live.Topic{Kind string, Presence bool, Authorize func(ctx context.Context, key string) error}`.
    `Presence` says whether clients may announce presence on it. `communications.inbox`
    and `knowledge.tree` say no. The type lives in `srv/live`, which imports only
    `contracts` and `db`, so `module → live` forms no cycle.
    `Kind` is `<module>.<kind>`, and the module prefix must equal the module's name, the
    way permission keys do (`contracts.ValidatePermission`). `Compose` collects the
    topics of enabled modules and refuses duplicates.
  - **Authorising.** `Authorize` runs when a client subscribes, with a context carrying
    `contracts.AsUser(ctx, principal.UserID)`, so `contracts.HasPermission` answers
    through `Access.UserHolds`. It returns `nil` or `live.ErrForbidden`.
    - **No existence oracle.** A key that does not exist and a key the person may not see
      are both `forbidden`. `unknown_topic` is only for a kind no enabled module
      registered. A key-level check, such as whether this person may see conversation
    4711, is the module's own and uses the same rule its `GET` uses.
  - **Disabled modules.** A topic of a disabled module is `unknown_topic`.
  - **Wiring.** `Compose` collects the enabled modules' topics, and the resolved
    `contracts.UserDirectory`, into a `*live.Registry` passed in through `Deps.Live`.
    `cmd/vantigo` builds the hub from that registry and hands its handler to
    `server.Options.Live`. `live.Notify` stays a free function (L6), so a module needs
    neither the hub nor the registry to notify.
- **L10 — The hub's fan-out never blocks.**
  - **Routing.** The hub keeps `topic → subscriptions` in memory. On a notification it
    puts the event on each subscribed connection's send queue.
  - **Slow clients.** The queue is bounded (64 frames). A connection whose queue is full
    has its queue cleared and a single `resync` queued, and is closed with 1008 if that
    happens three times in a minute.
  - **Writing.** One goroutine per connection writes. Reads have their own goroutine.
- **L11 — Authorisation is checked again, every 60 seconds, without keeping the session
  alive.**
  - **Why not `Access.Check`.** `Access.Check` slides the session's idle window
    (`identity/access.go:299,307-316`). A socket re-checked every 60 s would keep a
    hidden tab's session alive for ever and defeat `SESSION_PRIVILEGED_IDLE_TIMEOUT`.
  - **The new method.** `contracts.Access` gains `SessionLive(ctx, sessionID) (userID
    uuid.UUID, ok bool, err error)`. It is a non-sliding read, implemented by identity
    and used only by the hub. A session that is gone, expired or idle-expired closes the
    socket with 4401. The client reconnects only if the person is still signed in, so a
    tab left alone is signed out on schedule.
  - **Subscriptions.** Then every subscription's `Authorize` is re-run, with
    `UserHolds` answers cached per user and key for one cycle, so 200 subscriptions do
    not cost 200 permission reads. One that now fails is dropped with `denied`.
  - **Why 60 seconds is enough.** Because events carry identifiers only (L5), the window
    leaks at most that something with a given id changed. The data itself is always
    fetched through a checked request.
- **L12 — Limits and keep-alive.**
  - **Per connection:** 200 subscriptions.
  - **Per user and replica:** 20 connections, roughly a tab each. The 21st is refused
    with 4429.
  - **Keep-alive.** The server sends a WebSocket ping every 25 s and closes a connection
    that has not answered in 60 s. This also keeps proxies with a 60 s idle timeout from
    cutting it.
  - **Shutdown.** `http.Server.Shutdown` does not touch hijacked connections. The hub
    therefore takes the process's shutdown context and, when it is cancelled, does two
    things before `Shutdown` returns and before `closePool`:
    - it closes every socket with 1001 (going away);
    - it publishes its members' leaving on `context.WithoutCancel`. Clients reconnect to
    another replica (L13).

### Presence

- **L13a — Presence is per topic, kept in memory on every hub, and shared between hubs in
  batches.**
  - **Locally.** A client's `presence` frame requires a subscription to a topic whose
    `Presence` is true. It updates the hub's `topic → connId → member` map at once, and
    the hub pushes the topic's member set to its own subscribers at once.
  - **Between hubs.** Every 3 s, each hub sends **one** notification with the deltas
    since the last one (`{"k":"presence","h":hubId,"d":[{t,u,s}…]}`, chunked under
    7 KiB). It sends it on its own second dedicated connection, the presence publisher,
    never on the pool.
    - Every 15 s the hub repeats its full member set as a heartbeat.
    - A remote hub's members expire 45 s after its last batch or heartbeat. A hub that
      dies therefore leaves no ghosts.
    - Clients repeat their state every 20 s, to the local hub only.
  - **Names.** Each hub resolves user ids, local or remote, through a cache over
    `contracts.UserDirectory.Users` (10 min). The payload carries only the user id.
  - **Pushing.** A hub pushes the whole member set to a topic's subscribers whenever it
    changes, at most every 500 ms per topic.
  - **One person in many tabs.** The client collapses members by user, so the same person
    shows once, with the "strongest" state (editing > replying > viewing).
  - **Load.** Presence is per hub, not per client, so a hundred people watching costs
    one notifying commit per replica every 3 s at most.

### The browser

- **L13 — The shell owns one socket per tab.**
  - **Where it lives.** `packages/frontend-shell` gains `LiveProvider`, `useLiveTopic`,
    `usePresence` and `useLiveStatus`.
  - **Where the host mounts it.** Once, **under the signed-in layout**, keyed on the
    session's user id. The sign-in and pay pages then open no socket, and signing in as
    someone else in the same tab reconnects.
    The shell takes no react-query dependency. A module's callback calls
    `queryClient.invalidateQueries` itself.
  - **`useLiveTopic(topic, onEvent, {enabled})`** subscribes while mounted. Two components
    on one topic share the subscription. `onEvent` receives `{kind, id}` or
    `{kind: "resync"}`, and its calls are debounced per topic (250 ms trailing, with
    distinct ids gathered).
  - **`usePresence(topic, state)`** sends the state and returns the collapsed members,
    without the person themselves.
  - **`useLiveStatus()`** is `connecting | live | degraded | unavailable`. Only `live`
    turns fallback polling off.
  - **Reconnecting.** On a lost connection the provider reconnects with backoff (1 s
    doubling to 30 s, with jitter), re-subscribes everything, and hands every subscriber a
    `resync`.
  - **Unavailable.** After five failed attempts the status is `unavailable`, and the
    provider keeps trying every 60 s. This covers `LIVE_ENABLED=false` and a proxy that
    refuses upgrades.
  - **Fallback polling.** A screen made live keeps a fallback poll used only while the
    status is not `live` (Communications: 60 s).
- **L14 — What else changes for the browser.**
  - The host's Vite dev proxy gets `ws: true`.
  - The CSP keeps `connect-src 'self'`, which CSP3 browsers apply to same-origin `ws:` and
    `wss:` too. Delivery 0 verifies this in the browser and adds the explicit scheme only
    if one of the supported browsers needs it.

### Operating it

- **L15 — Operator documentation.**
  - `admin/installation.md` (en and nb) gains *Live updates and your proxy*:
    - pass `Upgrade` and `Connection` for `/api/v1/live`;
    - a read timeout of at least 75 s;
    - nginx and Caddy examples;
    - the two extra database connections per replica;
    - poolers, and `LIVE_DATABASE_URL`;
    - `LIVE_ENABLED`;
    - how to verify: the browser's network tab shows `101 Switching Protocols`.
  - `admin/configuration.md` lists `LIVE_ENABLED` and `LIVE_DATABASE_URL`.
  - `contributing/platform-packages.md` adds `apps/server/internal/live` to its
    `sources`, so the docs check enforces the page.
- **L16 — Telemetry.**
  - Gauges: `vantigo.live.connections` and `vantigo.live.subscriptions`.
  - Counters: `vantigo.live.notifications`, `vantigo.live.resyncs{reason}`,
    `vantigo.live.denied{reason}`.
  - The listener's state goes in the health detail on the management listener.

### The first live screen: the Communications inbox

- **L17 — Topics.**
  - **`communications.inbox`** is authorised by `communications:conversations-view`.
    Event `conversation.changed`, with the conversation's id.
  - **`communications.conversation:<id>`** is authorised by the same rule `GET
    /communications/conversations/{id}` applies to that conversation. Events
    `conversation.changed` (status, assignment, tags, links), `message.added`, and
    `message.changed` (delivery outcome, attachment scan status). The 2 s scan poll then
    runs only while live is not `live`. Presence is allowed on this topic.
- **L18 — Where Communications notifies.** It notifies on the transaction of every write
  that changes what the inbox or a conversation shows. At the time of writing these are:

  | Area | Where (`srv/communications/`) |
  | --- | --- |
  | Inbound mail | `inbound_import.go` `importMessage` |
  | Messenger | `messenger_import.go` `importMessage` |
  | New conversation | `conversations_create.go` |
  | Reply | `conversations_reply.go` `queueReply` |
  | Retry | `conversations_retry.go` |
  | Note (on the pool) | `conversations_notes.go` |
  | Status, assignment, other fields | `conversations_patch.go` |
  | Read state | `PostCommunicationsConversationsByIdRead` |
  | Tags | `tags.go` put and delete |
  | Customer link and candidates | `inbound_linker.go`, the AI customer suggestion |
  | Mail delivery outcomes | `outbox.go` `complete`, `markFailed`, `cancelSuppressed` |
  | Messenger delivery outcomes | `outbox_messenger.go`, including `reapplyWatermarks` |
  | Sync giving up | `mailbox_sync.go`, `messenger_sync.go` `giveUp` |
  | Marks | `mailbox_marks.go` |
  | Attachment scan status | the scan worker |
  | Deletions | `retention.go`; `customer_personal_data.go` `EraseCustomerData` (on Customers' transaction) |

  Bulk paths (retention, erasure) send one `communications.inbox` event without an id
  (L6). Each path is listed in the reference page.
  - **A test keeps the list honest.** It walks the `store` write methods on conversations
    and messages and requires each caller to notify. Delivery 0 rebases onto the
    Messenger stack's B and C if they land first, since it touches `messenger_import.go`
    and `outbox_messenger.go` too.
- **L19 — The screens.**
  - **The list.** The inbox list invalidates `["conversations"]` on any inbox event.
  - **The open conversation.** It invalidates `["conversation", id]` on its own topic.
  - **Presence.** The open conversation shows who else is in it: "Also here: Kari", or
    "Kari is replying" when the composer of another person has focus and text. This
    guards against two people answering the same customer.
  - **The side panel.** The conversations side panel (Communications delivery E, its own
    PR) uses the same hooks once both have landed. Whichever lands second wires it.

## 3. Decisions — delivery K-A, articles

### The module

- **K1 — `knowledge`, Norwegian *Kunnskap*, package `@vantigo/knowledge-ui`, route
  `/knowledge`.**
  - **Requirements.** It requires no other module.
  - **Registries.** It is registered in every registry of MB's *Adding a module* and the
    list in `AGENTS.md`.
  - **Schema.** Its schema is `knowledge`.
- **K2 — Permissions:**
  - **`knowledge:articles`** — use Knowledge: read and write articles **as each article's
    access allows** (K4).
    - **Who holds it.** No role is granted it by the migration (MB step 4: a baseline
      owns only its own schema). As with Inventory and Orders, nobody but the Owner can
      use the module until an administrator grants it to a role. The user guide says so.
      Open question 5 asks whether a default grant is wanted.
  - **`knowledge:manage`** — administer:
    - see and change the access of every article that is not private;
    - restore anyone's trashed article;
    - empty the trash.
    - It does **not** open private articles. A private article is private from
      administrators too, as in Odoo.

### Articles and the tree

- **K3 — An article is a page in a tree.**
  - **Columns:**
    - `id` (uuid);
    - `parent_id` (nullable);
    - `position` (a fractional-index text key, so a move never renumbers its siblings);
    - `title`;
    - `icon` (one emoji, optional);
    - `content` (jsonb, the ProseMirror document, K8);
    - `content_text` (the derived plain text);
    - `search` (a generated `tsvector`, K12);
    - `version` (integer, the concurrency token);
    - `locked`;
    - `ai_hidden` (K16);
    - `created_by`, `created_at`, `updated_by`, `updated_at`;
    - `trashed_at`, `trashed_by`, `trash_root` (K10).
  - **Moving.** A move names the new parent and the sibling to come after. The server
    computes the key, and refuses a move under the article's own descendant
    (`400 move_into_descendant`).
  - **Depth.** The tree is limited to 10 levels (`400 too_deep`), which keeps the
    sidebar sane and the recursive queries small.

### Access

- **K4 — Access follows Odoo's model: a default for everyone, explicit members, both
  inherited down the tree.**
  - **The default.** An article has `internal_permission`: `write`, `read` or `none`. It is
    nullable, and null means inherited from the parent. A root's is never null.
  - **Members.** `knowledge.article_members (article_id, user_id, permission write|read|none)`
    holds explicit members. These are inherited down the tree too. A member row on a
    descendant overrides the inherited one, so `none` can shut one person out of a
    subtree.
  - **A page with its own default is detached** (revision 3, the K-A review): an article
    whose `internal_permission` is set, not inherited, stops ancestors' member rows from
    reaching it, as Odoo's desynchronised articles do. Below it, only its own members and
    its descendants' members apply. Otherwise, adding oneself as a member on a workspace
    page would open every private page under it, administrators included.
  - **What a person may do.** It comes from the nearest member row for them on the path
    from the article up to the first detached article (inclusive) or the root. Failing that, it comes from the nearest
    `internal_permission`. It is always also bounded by the `knowledge:articles`
    permission.
  - **The sidebar's sections are derived, not stored:**
    - **Workspace:** the topmost readable articles whose effective default is not `none`;
    - **Shared:** the topmost articles readable through a membership whose effective
      default is `none` and which the person does not own;
    - **Private:** roots whose default is `none` and whose only writer is the person;
    - **Favourites:** K11.
  - **Creating.** A new root is created in *Workspace* (`internal_permission = write`) or
    in *Private* (`none`, with the creator as the `write` member). A child inherits.
  - **Who may change access.** A writer of an article may change its default and its
    members (Odoo's rule), and so may `knowledge:manage` on non-private articles. The
    last writer cannot be removed (`400 last_writer`).
  - **Moving between sections.** Moving an article from Private into Workspace, or the
    reverse, asks for confirmation in the interface, because it changes who can read it.
    The API takes `confirmVisibility: true` and otherwise answers
    `409 visibility_change`.
- **K5 — Effective access is materialised, recomputed in the same transaction that
  changes it.**
  - **Tables:**
    - `knowledge.article_access_default (article_id, permission)`;
    - `knowledge.article_access (article_id, user_id, permission)`.
  - **When they are recomputed:** for the affected subtree on every move, every change
    of a default or `ai_hidden`, every member change, trash and restore.
  - **One lock for the tree.** Every such write first takes
    `pg_advisory_xact_lock(hashtext('knowledge.tree'))`, then re-reads the path it
    depends on (the cycle check of a move included).
    - Locks per subtree would not serialise overlapping subtrees. Two crossing moves
      could form a cycle, and an ancestor's change could be overwritten by a descendant's
      recomputation that read the old value.
    - Tree writes are rare next to content saves, which never take the lock, so one
      lock is cheap.
  - **Why.** Reads become a join, not a recursive walk. Search (K12) filters thousands of
    articles in one query, and the Assistant's calls stay cheap.
  - **Proof.** A test compares the materialised tables with a recursive reference
    computation over random trees. Concurrency tests run crossing moves, and an
    ancestor change racing a descendant member change.
- **K6 — What a request is checked against.**
  - **No read access.** An article the person cannot read answers `404`, never `403`,
    so its existence is not revealed.
  - **Read but not write.** A write to an article the person can read but not write
    answers `403 article_read_only`.
  - **Locked.** A locked article answers `409 article_locked` to content edits. Any writer
    can lock and unlock.

### Content and the editor

- **K7 — The editor in K-A** is TipTap 3 with:
  - StarterKit (headings 1–3, bold, italic, strike, inline code, bullet and ordered lists,
    quote, code block, divider, hard break);
  - Link;
  - task lists;
  - Placeholder;
  - a bubble menu for formatting;
  - Markdown input rules (`#`, `-`, `1.`, `[ ]`, `>`, three backticks).

  K-B adds the rest. The editor is a component of `@vantigo/knowledge-ui` built on
  `@mantine/tiptap`, read-only for readers and editable for writers.
- **K8 — The stored form is ProseMirror JSON. The server validates it and derives text
  from it. No HTML is ever stored.**
  - **Where.** A new package, `srv/knowledge/doc`, holds an allowlist of node and mark
    types and their attributes, mirroring the editor's schema:
    - a link's `href` must be `https:`, `http:`, `mailto:` or an application path starting
      with `/`;
    - unknown nodes, marks or attributes answer `400 invalid_document` with the JSON
      path;
    - a document over 2 MiB answers `413`.
  - **Derived text.** The same package derives `content_text` (block-separated plain
    text) and, in K-B, Markdown.
    - Article links and record chips (K21, K22) are rendered in `content_text` **without a
      resolved title**: only their own link text, or nothing. A title the reader may not
      see therefore never reaches a search excerpt or the Assistant.
  - **Why no sanitiser.** Because the browser renders the JSON through the editor's
    schema, and the server never emits HTML, no HTML sanitiser is needed. The allowlist
    replaces it.
  - **Keeping the two in step.** A test in each language compares the allowlist with the
    editor's schema, exported as JSON by a small script in the UI package.
- **K9 — Saving: autosave with a concurrency token, and coalesced versions.**
  - **Autosave.** The editor saves 1.5 s after typing stops:
    `PUT /knowledge/articles/{id}/content` with `{title, content, expectedVersion}`.
  - **Leaving the page.** A save there is best effort, because `fetch` keepalive is
    limited to 64 KiB. The editor therefore also keeps an unsaved draft in `localStorage`
    per article. It offers the draft back on the next open when that draft is newer than
    the saved version.
  - **A stale version.** It answers `409 version_conflict` with the current version's
    author and time. The editor then shows *"Kari changed this page while you were
    editing"*, with **Reload** (drop mine) or **Keep mine** (save again with the new
    version; the earlier one stays in the history).
  - **Versions.** `knowledge.article_versions (article_id, version, title, content,
    created_by, created_at)` keeps one row per save, coalesced. A save by the same person
    less than 10 minutes after their previous one replaces that row. Any other save adds
    a row.
  - **History.** It lists versions with author and time, shows one read-only, and
    **Restore** saves it as a new version. Versions are kept for 365 days. The newest 20
    of an article are kept regardless. Pruning is done by the `knowledge.retention`
    worker.
  - **Presence warns.** While someone else's presence on the article is `editing`, the
    page shows *"Kari is editing"* above the editor. It does not lock: the version token
    is what keeps saves safe. Live events (`knowledge.article:<id>`, `content.changed`)
    refresh a reader's view at once, but never an editor's own unsaved text. K-C
    replaces this with true co-editing.

### Trash, favourites, lock

- **K10 — The trash.**
  - **Trashing.** Trashing an article trashes its subtree in one transaction: every row
    gets `trashed_at` and `trash_root` = the trashed article.
  - **Seeing it.** Trashed articles are invisible everywhere but *Trash*, which lists the
    trash roots the person could write.
  - **Restoring.** **Restore** brings the whole subtree back under its old parent, or to
    the root of its old section when the parent is gone or trashed itself.
  - **Purging.** After 30 days, `knowledge.retention` deletes them for good. So does
    `knowledge:manage`'s **Empty trash**, after confirmation.
- **K11 — Favourites** are `knowledge.favourites (user_id, article_id, position)`,
  personal and ordered by drag in the sidebar. A favourite of an article the person can no
  longer read is not shown, and is removed by the retention worker.
- **K11a — The lock** is `locked`: a writer's **Lock page** makes the content read-only
  for everyone until a writer unlocks it. It is for policies and finished routines.

### Search

- **K12 — Full-text search in Postgres, in Norwegian and English at once, with
  typo-tolerant titles.**
  - **The index.** `search` is a stored generated column:

    ```sql
    setweight(to_tsvector('norwegian', coalesce(title, '')), 'A')
    || setweight(to_tsvector('english', coalesce(title, '')), 'A')
    || setweight(to_tsvector('norwegian', left(coalesce(content_text, ''), 200000)), 'B')
    || setweight(to_tsvector('english', left(coalesce(content_text, ''), 200000)), 'B')
    ```

    It has a GIN index. Both configurations are built into Postgres, and
    `to_tsvector(regconfig, text)` with a literal configuration is immutable, as a
    generated column needs.
    - **The cap.** A `tsvector` cannot exceed about 1 MB. Only the first 200 000
      characters of the text are therefore indexed. The page itself is stored whole. The
      reference page states the cap.
  - **No trigram index in phase 1.** As-you-type matching uses prefix terms (`:*`) on the
    last word. `pg_trgm` waits until search shows a need for typo tolerance. Without it
    the migration creates no extension, which also spares managed Postgres its extension
    allow-listing.
  - **The query.** `websearch_to_tsquery` in both configurations, OR'd, filtered by K5's
    access join and by trash. Ranking is `ts_rank_cd`, with the title weighted. Each hit
    gets a `ts_headline` excerpt.
  - **The endpoint.** `GET /knowledge/search?q=&limit=20` returns, per hit:
    - `{id, title, icon, path: [{id,title}], excerpt, updatedAt}`.
  - **`path` stops at what the reader may see.** It runs upwards and ends below the first
    ancestor the reader cannot read, and, for the Assistant, below the first hidden one.
    This is the case *Shared* exists for: a child readable under an unreadable parent.
    The same rule applies wherever a path is returned (K14).
  - **The spotlight.** The host's spotlight gets a Knowledge source, so `Ctrl/Cmd+K` finds
    articles.
  - **Why not embeddings yet.** Semantic search with embeddings is phase 2 (§13). It is
    measured against this, not assumed.

### Live in K-A

- **K13 — Topics:**
  - **`knowledge.article:<id>`** — readers of the article. Events `content.changed`,
    `meta.changed` (title, icon, lock, access), `trashed`. Presence: `viewing`,
    `editing`.
  - **`knowledge.tree`** — anyone with `knowledge:articles`, no presence. Event
    `tree.changed` with **no id**: the client refetches its own sidebar. An id on this
    topic would tell everyone that a hidden article exists.
    - **What sends it.** It is sent only when something the sidebar shows changes: title,
      icon, parent, position, access, trash or restore. A content save whose title is
      unchanged does not send it, so autosave never makes everyone refetch.

### The Assistant

- **K14 — The Assistant reads through its ordinary loop:** `search_operations` →
  `describe_operation` → `call_operation`, as the person.
  - **Mode.** Two operations are marked `x-vantigo-assistant: auto`:
    - `searchKnowledge` (K12);
    - `getKnowledgeArticleText`: `GET /knowledge/articles/{id}/text?offset=`. It returns
      `{id, title, path, text, nextOffset}`, the Markdown-ish plain text in pieces of
      12 000 characters, so a long article is read in parts and never in raw JSON.
  - **What the Assistant does not get.** **Every other Knowledge operation is marked
    `x-vantigo-assistant: never`**, reads included:
    - the JSON `GET`, the tree, versions, access, the trash, favourites;
    - K-B's files and backlinks;
    - every write (§13: the Assistant writing articles).

    Since `mode.go` makes an unmarked `GET` `auto`, this is explicit, and a contract test
    pins that only these two are `auto`. The Assistant thus reaches articles only through
    the two operations that apply `ai_hidden`.
- **K15 — The Assistant's prompt.**
  - **Answers cite.** The prompt names Knowledge as the place for the company's own
    routines, and tells the model to cite the article it answers from, with a link
    (`/knowledge/articles/{id}`, added to `appRoutes` and `APP_ROUTE_PREFIXES`).
    `promptVersion` is bumped.
  - **Content is data.** The rule that tool results are data, never instructions,
    already exists (`prompt.go:95`). Article text is a tool result, so a page that says
    "ignore your instructions" is covered.
- **K16 — Hidden from the Assistant.** `ai_hidden` on an article hides it and its whole
  subtree. It is **monotone**: a descendant cannot un-hide itself, so a hidden parent's
  title never surfaces through a child's `path`. It is materialised in
  `article_access_default.ai_hidden`.
  - **Effect on the Assistant.** When the principal's `Via.Kind` is `assistant`, search
    leaves hidden articles out and the text operation answers `404`.
  - **Who sets it.** A writer of the article sets it.
  - **What it is not.** It is a choice about the Assistant only, not an access rule: the
    person still reads the article themselves.

### Erasure

- **K17 — A user who is erased.**
  - **How it is found.** It is found the way the Assistant finds one (`assistant/erasure.go`'s
    `sweepErased`): `knowledge.retention` asks `contracts.UserDirectory.Users` in chunks
    of 200 for every user id that Knowledge holds.
  - **What is removed:**
    - their member rows;
    - their favourites;
    - the private articles where they were the only writer, together with the subtrees
      and versions of those articles.
  - **Authorship.** `created_by`, `updated_by` and the versions' authors are set to null
    and shown as *"Former user"*. The text they wrote in shared articles stays: it belongs
    to the company's knowledge, as an invoice line written by a former employee does.

## 4. Decisions — delivery K-B, the editor's comfort

- **K18 — The blocks and tools.**
  - **The slash menu**, from `/` or the `+` beside an empty line, offers every block with
    a Norwegian and an English name and keyword.
  - **Drag handles** move a block, with `@tiptap/extension-drag-handle(-react)` (MIT).
    Blocks get stable ids from `@tiptap/extension-unique-id` (MIT), which K-C's cursors
    and anchor links reuse.
  - **Callouts** (info, tip, warning, danger, with an emoji) and **toggles** (`details`)
    are custom nodes.
  - **Tables** use TipTap's table extensions: column resize, header row, and adding or
    removing rows and columns from a cell menu.
  - **Code blocks** get syntax highlighting with lowlight. The language is chosen per
    block.
  - **A table of contents** block lists the headings and links to them.
  - **Only permissively licensed packages** (MIT, BSD, Apache-2.0). The `@tiptap-pro`
    packages are not used. Every extension's licence, and its dependencies' licences, are
    checked when it is added and listed in the reference page. `highlight.js` (through
    lowlight) is BSD-3-Clause. The drag handle's peers bring Yjs, which K-C uses anyway.
- **K19 — Images and files.**
  - **How they arrive.** Paste, drag-drop or the slash menu upload to
    `POST /knowledge/articles/{id}/files` (multipart, 25 MiB, writers only).
  - **Storage.** They are stored through `storage.NewScope(…, "knowledge")` with a random
    key, in `knowledge.article_files`.
  - **Serving.** They are served by `GET /knowledge/files/{fileId}`, behind the article's
    read access, with `nosniff` and `Content-Disposition: inline` for images only.
  - **Images.** Only JPEG and PNG are images: they pass through `imagemeta`'s stripper,
    as product images do, and it handles only those (`imagemeta.go:35-43`).
    - Every other type, SVG included, is an attachment, served as a download, never
      inline.
    - The allowlist (K8) limits an image node's `src` to `/api/v1/knowledge/files/…`.
  - **Removal.** A file no version references any more is removed by the retention
    worker after the versions that held it are pruned.
- **K20 — `@mentions`** of people use the Users directory's search. A mention is stored
  as the user id and rendered with the current name. Being mentioned sends a
  notification through `UserDirectory.Notify`, once per person per article per hour.
- **K21 — Links between articles.**
  - **Typing one.** `[[` opens an article picker and inserts a link node holding the
    target id **and no title**. The server clears any label on save (revision 4), because a
    stored title would reveal a private target to every reader of the page, and through
    export and the API. The link renders the target's current title, or *"No access"*,
    or *"Deleted"*.
  - **Backlinks.** `knowledge.article_links (from_id, to_id)` is rewritten on every save.
    *Linked from* at the foot of a page lists the backlinks the reader may read.
- **K22 — Links to Vantigo records.** Pasting a Vantigo URL (a customer, a project, an
  order, an invoice) turns it into a record chip that holds the path.
  - **Which paths.** Only paths whose first segment is an enabled module's route, and
    never a URL with a query or fragment, which stays ordinary link text (revision 4).
  - **The label** is the module's name and the route's last segment. The Assistant's
    route table is not reachable from a module UI, so it is not used. The chip
  shows only what the person may open, because opening it is an ordinary navigation.
- **K23 — Markdown.**
  - **Export.** **Export → Markdown** downloads the article, or the article with its
    subtree as a `.zip`. Images go in an `assets/` folder.
  - **Import.** **Import Markdown** turns one `.md` file, or a `.zip` from Obsidian or a
    Notion export, into articles under a chosen parent. Callouts in Obsidian's `> [!note]`
    form become callouts, and `[[wiki links]]` resolve between the imported files.

## 5. Decisions — delivery K-C, live co-editing

This section is a sketch. K-C gets its own revision of this spec before it starts.

- **K24 — Yjs in the browser, opaque updates on the server.**
  - **The editor.** TipTap's `Collaboration` and `CollaborationCaret` extensions,
    with a small provider of our own on the shell's socket: binary frames for topic
    `knowledge.doc:<id>`. The provider speaks y-protocols' sync and awareness messages.
  - **The server relays and stores.** It stores updates in
    `knowledge.article_updates (article_id, seq, data bytea)`, sends a joiner the stored
    rows, relays new updates through NOTIFY (an id only) and the table, and drops
    updates from readers.
  - **Compaction.** A browser sends the merged state up to `seq` *N* every 200 updates.
    The server swaps rows ≤ *N* for it, in one transaction.
  - **Search and history.** They still come from the debounced JSON save of K9, which
    keeps K-A and K-B unchanged.
  - **The first live open.** It converts the stored JSON with `prosemirrorJSONToYDoc`.
  - **The fallback.** Yjs inside Go (`yrs` compiled to WebAssembly under `wazero`) is the
    fallback, if browser-side compaction proves fragile. A spike decides that before K-C
    is planned.

## 6. Permissions summary

| Request | Checked against |
| --- | --- |
| Open `/api/v1/live` | A valid session (L2) |
| Subscribe to a topic | The topic's `Authorize` (L9) |
| List the tree, search, read an article | `knowledge:articles` and the article's read access (K4–K6) |
| Edit content, title, icon, lock, trash, files | `knowledge:articles` and write access |
| Change an article's access or `ai_hidden` | Write access, or `knowledge:manage` on non-private articles |
| Restore another person's trash, empty the trash | `knowledge:manage` |

## 7. Data model (schema `knowledge`, K-A)

| Table | Holds |
| --- | --- |
| `articles` | K3's columns. Indexes: `(parent_id, position)` where not trashed, GIN on `search` |
| `article_members` | `(article_id, user_id) → permission` |
| `article_access_default` | Materialised `(article_id) → permission, ai_hidden` (K5, K16) |
| `article_access` | Materialised `(article_id, user_id) → permission` |
| `article_versions` | K9 |
| `favourites` | K11 |

K-B adds `article_files` and `article_links`. K-C adds `article_updates`. Delivery 0 adds
no table. Migration numbers: K-A claims `00135_knowledge_baseline` and K-B
`00136_knowledge_files_links`. Main is at `00131`, and the Messenger stack holds
`00132`–`00134`. They are renumbered right before merging if main takes them.

## 8. Platform, registries and documentation

**Delivery 0:**

- **Server:**
  - `srv/live`, with the `LiveTopics` slot in `srv/module` and `Deps.Live`, so that a
    module can reach `live.Notify` without importing the hub.
  - `server.New` mounts the endpoint, and the depguard `platform` set lists it.
  - `cmd/vantigo` builds the hub in `api` and `server` modes and passes it the shutdown
    context.
- **Frontend:** `packages/frontend-shell` gains the provider and hooks, and the host's
  `main.tsx` and Vite config change.
- **Docs:**
  - `contributing/platform-packages.md` (*Live updates*);
  - `contributing/frontend-architecture.md` (*Live screens*);
  - `contributing/module-boundaries.md` (the slot);
  - `admin/installation.md` and `admin/configuration.md` (en and nb);
  - `reference/communications.md` (topics and notify points);
  - `user/communications.md` (en and nb): the inbox updates by itself, and who else is in
    a conversation.

**K-A:**

- **Registries:** every one in AGENTS.md's list.
- **Deployment:** the `knowledge` schema in `deploy/compose/README.md`'s grant lists and in
  `admin/installation.md#database-roles`, with a release note.
- **Docs:**
  - `user/knowledge.md` (en and nb): tasks: write a page, organise the tree, share,
    lock, history, trash, search, favourites;
  - `reference/knowledge.md`: the access model with its rules and status codes, the
    topics, the workers, the Assistant's operations;
  - `reference/assistant.md`: Knowledge as a source;
  - the overviews of `user/` and `reference/`.
**Every delivery** that edits a docs page runs `go generate ./...` in `apps/server`
afterwards and commits the regenerated copy.

**K-B and K-C** update the same pages.

## 9. Edge cases

- **The listener is down for long.** Events are lost. Every socket gets `degraded`, so
  screens poll. The hub reconnects, and every socket gets `resync`. A screen refetches. Nothing is stored for replay, because nothing
  needs replaying (L5).
- **A worker-only process notifies while no HTTP replica listens.** Postgres drops the
  notification, and the next page load shows the state. Correct.
- **The same person has the article open in two tabs and edits in both.** The version
  token makes the second save meet `409`. The *Keep mine* path resolves it, and presence
  shows the person as *editing* only once.
- **A member's access is revoked while they read.** The next fetch answers `404`. The
  page shows *"You no longer have access to this page"*, and their subscription drops
  within 60 s (L11).
- **Moving a subtree under a private article.** It requires `confirmVisibility`. K5
  recomputes the subtree in the move's transaction.
- **A huge paste.** A document over 2 MiB is refused with `413`. The editor shows the
  limit and keeps the local text.
- **A trashed parent with a restored child.** Restoring a child whose trash root is its
  ancestor restores the ancestor's whole trash group, and says so. A child cannot leave
  its trash group alone.

## 10. Testing

**Live:**

- **The hub, without a socket:** routing, slow consumers, limits, presence expiry,
  re-authorisation.
- **Two hubs against the test database** (two listeners):
  - `Notify` in a committed transaction reaches both;
  - a rolled-back one reaches neither;
  - a killed listener connection yields `resync`;
  - a denied subscription never receives.
- **An HTTP test** for origin (`Origin` and `Sec-Fetch-Site`, missing origin), session, the
  `LIVE_ENABLED` switch and shutdown codes.
- **`SessionLive`:** it does not slide the idle window, and it closes an idle-expired
  socket.
- **A `DATABASE_URL` carrying `pool_max_conns`** still brings the listener up.
- **Shell hook tests** with a fake WebSocket: debounce, shared subscriptions, reconnect
  with `resync`, the fallback status.
- **Communications:** a test per write path that the topic is notified (L18), and the
  inbox page's live invalidation.

**Knowledge:**

- **The access computation** against a recursive reference over random trees (K5).
- **Every status code of K6.**
- **The document allowlist** against the editor's exported schema (K8).
- **Version coalescing.**
- **Trash group restore.**
- **Search:** in both languages, and its access filter.
- **The Assistant:** `ai_hidden` with a `Via.Kind = assistant` principal; a contract test
  that only `searchKnowledge` and `getKnowledgeArticleText` are `auto`; `path`
  truncation.
- **The tree lock:** crossing moves, and the ancestor/descendant race (K5).
- **An article over 1 MB of text** saves and is searchable on its first 200 000
  characters.
- **Erasure.**
- **The integration test's `everyModule()`.**

**The browser check:** two browsers, two people. One edits while the other watches, the
presence shows, and the inbox updates when a mail arrives.

## 11. Configuration

| Setting | Default | Where |
| --- | --- | --- |
| `LIVE_ENABLED` | `true` | Environment (operator), L2 |
| `LIVE_DATABASE_URL` | unset (uses `DATABASE_URL`) | Environment (operator), L7 |
| Trash retention | 30 days | Constant in K-A |
| Version retention | 365 days, newest 20 always | Constant in K-A |

Neither retention is a setting in phase 1. If a customer asks, they become workspace
settings.

## 12. Open questions for the owner

These are not blocking: each has a default that the plan follows until answered.

1. **Groups as members** (Entra or Google groups, Vantigo roles). Default: people only in
   phase 1.
2. **Team spaces** in the sidebar besides Workspace, Shared and Private. Default: no.
   A workspace root with members gives the same effect.
3. **The Assistant writing and editing articles** (drafting a routine from a
   conversation). Default: read only in phase 1.
4. **Embeddings.** Which provider is acceptable (OpenAI or Anthropic partners, a local
   model), or Postgres only. Default: Postgres full-text search only.
5. **A default grant of `knowledge:articles`** to every role, for example through the
   Owner's first-run setup. Default: none, as with Inventory and Orders. An
   administrator grants it.

## 13. Later, and what each waits for

| Later | Waits for |
| --- | --- |
| Embeddings and semantic or hybrid search (pgvector) | Real content to measure K12 against; the owner's answer 4 |
| The Assistant drafting articles | Answer 3; then confirm-mode writes with a diff card |
| Groups as members, team spaces | Answers 1–2; Identity groups |
| Inline comments | K-C (anchors survive edits only with Yjs relative positions) |
| Templates, item views, properties | Use; probably from Projects' templates experience |
| Publishing to the web | A public pages host (Invoices' pay host is the precedent) |
| Import from Confluence, Notion API, Google Drive | K-B's Markdown import |
| Per-user notification topic (`user`) | A notifications design in the host |

## 14. Revision 2 — the critic review, 2026-11-07

An Opus critic checked revision 1 against the code. What changed:

- **K5:** one advisory lock for every tree write. Locks per subtree did not serialise
  crossing moves, or an ancestor's change racing a descendant's.
- **K12:**
  - the `tsvector` input is capped at 200 000 characters and `coalesce`d, since a
    `tsvector` is limited to about 1 MB;
  - `pg_trgm` is cut from phase 1.
- **L11:** `SessionLive`, a non-sliding session read. `Access.Check` slides the idle
  window, which would have kept hidden tabs signed in for ever.
- **L7:**
  - the connection is built from `pgxpool.ParseConfig`, because of `pool_max_conns` on
    `DATABASE_URL`;
  - a `degraded` state while the listener is down;
  - `LIVE_DATABASE_URL` for poolers.
- **L13a:** presence is batched per hub every 3 s on a dedicated connection, because of
  Postgres's cluster-wide notify commit lock. Remote members' names come from a
  directory cache.
- **K14, K16:** every Knowledge operation but two is `never`; `ai_hidden` is monotone.
- **K12, K8:** `path` stops at what the reader may see; link titles never enter
  `content_text`.
- **K2:** no default grant of the permission (open question 5).
- **L9:** how the topics reach the hub (`live.Registry` through `Deps.Live`); denial
  gives no existence oracle; topics declare `Presence`.
- **L18:** the full list of Communications' write paths, and a test that keeps it honest.
- **K13:** `tree.changed` only when the sidebar changes.
- **Minor:**
  - **L2:** the origin rule and the `otelhttp` exclusion;
  - **L13:** the provider sits under the signed-in layout;
  - **L12:** per-replica limits and shutdown order;
  - **K9:** the local draft on leaving;
  - **K19:** only JPEG and PNG are images; `src` limited to the module's files;
  - **§8:** grants, migration numbers, `go generate` per delivery.
- **Kept, against the review's suggestion to cut:**
  - **presence in delivery 0:** it is half of what the owner asked the live channel for,
    and it guards Communications against double replies;
  - **favourites and the lock:** cheap, and Odoo's users expect them.

### Revision 3 — the K-A code review, 2026-11-07

- **K4:** a page with its own default is detached from its ancestors' members. Under
  revision 2, any writer of a workspace page could add themselves as a member there and
  so open a private page beneath it.

### Revision 4 — the K-B code review, 2026-11-07

- **K21:** article links store no title.
- **K19:** a pasted file from another article is copied into this one
  (`copyKnowledgeFile`), never referenced across articles.
- **K22:** record chips are limited to enabled modules' routes.
- **K23:** export streams, and import bounds its memory and its concurrency.
- **K18:** permissive licences (MIT, BSD, Apache-2.0), each listed.
