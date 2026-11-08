# Knowledge and live updates — the `live` platform package and the Knowledge module (phase 1)

Revision 5, 2026-11-08 (K-C and K-D designed; before that, an Opus critic review and the K-A and K-B code reviews; §14 lists what changed). Drawn from the owner's conversation of 2026-11-07, which agreed:

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
| **K-C** | **Live co-editing.** Yjs through TipTap's Collaboration extensions, an opaque update log, seeding, compaction, cursors | K-B, 0 |
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

Revision 5, 2026-11-08. This replaces the sketch of revisions 1–4. The server code of
delivery 0 and K-A is read as shipped (`srv/live`, `srv/knowledge`); a decision that
changes one of their rules says so.

### The shape

- **K24 — Yjs in the browser, opaque updates on the server, no Yjs in Go.**
  - **The editor.** TipTap's `Collaboration` (`@tiptap/extension-collaboration`, option
    `document`, and `field`, default `default`) and `CollaborationCaret`
    (`@tiptap/extension-collaboration-caret`, options `provider` and `user: {name,
    color}`, the colour `#RRGGBB`), over `@tiptap/y-tiptap` (3.2.0, already in the
    lockfile as the drag handle's peer), `yjs` 13.6 and `y-protocols` 1.0. All are MIT.
    `lib0` (MIT) comes with them.
  - **The server relays and stores bytes.** It never decodes a Yjs update. The merge is
    the browsers' standard Yjs merge.
  - **Why not `yrs` under `wazero` (rejected for phase 1).** A WebAssembly Yjs in Go would
    let the server compact and verify. It adds a runtime, a binary and a second
    implementation that must agree with the browsers' `yjs` byte for byte, to solve a
    problem (compaction) that a writer's browser solves with `Y.encodeStateAsUpdate`. It
    is revisited only if compaction proves fragile (K31, K35).
  - **What the protocol uses of y-protocols.** Only the **awareness** encoding
    (`encodeAwarenessUpdate`, `applyAwarenessUpdate`) and `Y.applyUpdate`. The sync
    handshake (`messageYjsSyncStep1`/`Step2`) is **not** used: it needs a peer that holds
    a state vector, and the server is not one. Its place is taken by K28.
  - **Search, history, links, mentions, the Assistant and export keep working off the
    JSON** (K32), so K-A and K-B are unchanged beneath.

- **K25 — The wire: binary frames on the shell's socket (revises L4).**
  - **Layout.** `kind` (1 byte), `topicLength` (2 bytes, big endian), `topic` (UTF-8, the
    L8 grammar, at most 80 bytes), `payload` (the rest).
  - **Kinds, client to server:**

    | Kind | Name | Payload |
    | --- | --- | --- |
    | `0x01` | update | a Yjs update (from `doc.on("update")`), at most 256 KiB |
    | `0x02` | seed | `Y.encodeStateAsUpdate` of the document built from the stored JSON (K29), at most 2 MiB |
    | `0x03` | awareness | y-protocols' awareness update, at most 4 KiB |
    | `0x04` | compact | `upTo` (8 bytes, big endian) then `Y.encodeStateAsUpdate` of the whole document, at most 2 MiB |

  - **Kinds, server to client:** `0x01` update: `seq` (8 bytes) then the update; `0x03`
    awareness: the update as received.
  - **JSON control frames** (text, as before). Client to server: `{"type":"doc_sync",
    "topic","after":seq}`. Server to client:
    - `synced {topic, seq}` — the log up to `seq` has been sent;
    - `ack {topic, seq}` — the sender's update, seed or compaction is stored at `seq`,
      in the order sent;
    - `update_rejected {topic, reason}` — `read_only`, `too_large`, `rate`, `log_full`;
      also in the order sent, in place of an `ack`;
    - `seed_accepted {topic}` and `seed_rejected {topic}`;
    - `compact_requested {topic, upTo}` and `compact_rejected {topic}`;
    - `doc_reset {topic}` (K32) and `doc_stale {topic}` (K28).
  - **Limits on the socket.**
    - The read limit becomes 4 MiB + header (`SetReadLimit`). The 4 KiB limit of L4 is
      then checked by the hub for **text** frames, which still close with 1009. A binary
      frame over its kind's limit closes with 1009 too. A malformed binary frame closes
      with 1007. A binary frame for a topic the connection is not subscribed to is
      dropped and counted.
    - The 1003 close for any binary frame goes: a binary frame for a topic without a
      `Doc` handler (below) is dropped and counted.
    - Update frames are limited per connection to 50 a second with a burst of 200
      (`update_rejected rate`). The browser batches (K34), so normal typing sends about
      ten a second.

- **K26 — Subscribing, and who may write (extends L9).**
  - **A new topic kind, `knowledge.doc`**, key the article id, no presence (the article
    topic keeps K13's presence). `live.Topic` gains an optional `Doc live.DocStore`: a
    module-supplied interface that gives `srv/live` the framing, the fan-out and the
    limits without it knowing a table:

    ```go
    type DocStore interface {
        // CanWrite: may this person's frames be stored (checked on subscribe and at every re-check).
        CanWrite(ctx context.Context, key string) bool
        // Replay streams the rows after `after` in seq order; last is the newest seq.
        Replay(ctx context.Context, key string, after int64, emit func(seq int64, data []byte) error) (last int64, err error)
        Append(ctx context.Context, key string, in DocWrite) (DocResult, error)
        Seed(ctx context.Context, key string, in DocWrite) (accepted bool, seq int64, err error)
        Compact(ctx context.Context, key string, upTo int64, in DocWrite) (ok bool, err error)
        // Fetch reads one row for a notification from another hub; found is false when compaction removed it.
        Fetch(ctx context.Context, key string, seq int64) (data []byte, found bool, err error)
    }
    ```

    `DocWrite` carries the user, the bytes and the **origin hub id**. `DocResult` carries
    the `seq` and whether compaction is now demanded (K31).
  - **Subscribing** is the normal `sub` (L4). `Authorize` is the article topic's: the
    permission and read access to an article that is not trashed. A reader subscribes
    and is sent the log (K28).
  - **Writing.** `CanWrite` is true when the person has write access, the article is
    not locked and not trashed. The hub keeps the answer per subscription and
    refreshes it in the 60-second re-check (L11), so a person who loses write access, or
    a page that gets locked, stops being stored within a minute. Update, seed, compact
    and awareness frames from a subscription that cannot write are **dropped silently
    and counted** (`vantigo.live.doc_dropped{reason="read_only"}`); an update frame
    additionally answers `update_rejected read_only` once per subscription so the editor
    can say so.
  - **Disabled collaboration.** If the topic is `unknown_topic` (module off) or the
    socket is not `live`, the editor works as K-A (K33).

- **K27 — Storage, in migration `00140_knowledge_coediting.sql`.**
  - **The table.** `knowledge.article_updates (article_id uuid references articles on
    delete cascade, seq bigint, data bytea not null, created_by uuid null, created_at
    timestamptz, primary key (article_id, seq))`. `seq` is **per article**, assigned as
    `max(seq) + 1` under the article's row lock, never reused, so compaction's range
    arithmetic (K31) is plain. `created_by` is set null for an erased user by the
    retention sweep (K17).
  - **Limits.**
    - One update is at most 256 KiB (a paste of an image is a file upload, K19, not an
      update).
    - **Compaction is demanded** when the log exceeds **4 MiB or 2000 rows**, and
      **updates are refused** (`log_full`) beyond twice that (8 MiB or 4000 rows) until
      a compaction lands.
    - A seed or a compacted row is at most 2 MiB, the same bound as a document (K8).
  - **Locks.** An append, a seed, a compaction and a reset each take the article's row
    lock (`LockArticle`, the one K9's content save takes) for their short transaction,
    **never the tree's advisory lock** (K5). They therefore serialise with a content
    save of the same article, and never with a move.
    - Lock order is the one K-A has: the tree lock (if at all), then the article row.
      Updates never take the first, so they cannot invert it.
  - **Notifying.** The append notifies on its own transaction (L6): topic
    `knowledge.doc:<id>`, kind `update`, id the `seq`, and the origin hub id in the
    payload's `h` (a field presence batches already use). Postgres delivers it on
    commit.

### The sync protocol

- **K28 — The server is not a Yjs peer; a joiner replays the log.**
  - **Joining.** After `ok`, the server streams the rows after 0 as `0x01` update frames
    in seq order, then `synced {seq}` with the newest. The subscription is registered
    **before** the log is read, and the read is one statement, so a row appended
    meanwhile is either in the replay or arrives as an event with a greater `seq`. An
    event with `seq` at or below the joiner's `synced` is dropped by the hub.
  - **Backpressure.** Replay frames go through a blocking enqueue (up to 10 s) and not
    through L10's drop-and-resync, since a 2000-row log would otherwise overflow the
    64-frame queue by itself. A socket that cannot take its replay in that time is closed
    with 1008.
  - **An update.** The hub stores it with `Append`, answers the sender `ack {seq}`, and
    puts the frame on the queue of every other subscription **on this hub**
    immediately, with the bytes in hand. It is **never echoed to the sender**.
  - **Other hubs.** Each hub that holds a subscription to the doc reads the row by
    `(article, seq)` with `Fetch` when the notification arrives and pushes it. The hub
    that appended skips its own notification (`h` equals its id). Fetches run on a
    per-doc worker, never in the listener's loop (L7), and coalesce: for a burst of
    `seq` *a..b* one `Replay(after = last pushed)` replaces *b − a* fetches.
  - **Yjs is idempotent and commutative**, so a duplicate, a reordering, or an update
    that arrives both in a replay and as an event, is harmless. The hub's duplicate
    filter only saves bandwidth.
  - **Missed events.** After a reconnect, a `resync` (L7), or a slow consumer's `resync`
    (L10), the provider sends `doc_sync {after: its newest received seq}` and receives
    the rows after it and a new `synced`. A row that compaction removed before a hub
    could fetch it (`found` false) sends the doc's subscribers `doc_stale`, with the
    same answer from the client; the compacted row has `seq` equal to its `upTo`, so it
    is greater than any `after` below it.
  - **The client's own unsent work.** The provider keeps each update it sent in a queue
    until its `ack`. On a reconnect it merges the unacknowledged ones with
    `Y.mergeUpdates` and sends them once, after `synced`. An `update_rejected` in place
    of an `ack` drops the queue for good (the editor goes read-only, K33).

- **K29 — Seeding: the first open converts the JSON, the server lets one win.**
  - **The case.** An article that was never co-edited has an empty log, and a stored
    ProseMirror JSON.
  - **The client** that writes, and sees `synced {seq: 0}`, normalises the fetched JSON
    through a throwaway editor with the same extensions but no `Collaboration` (so the
    block ids of K18 exist), builds the document with `prosemirrorJSONToYDoc(schema,
    json, "default")` from `@tiptap/y-tiptap`, and sends **one `seed` frame**. It holds
    the editor read-only, showing the fetched JSON, until the server answers.
  - **The server** accepts a seed **only if the log is empty**, in one transaction under
    the article's row lock: `insert … where not exists`. It answers `seed_accepted` (and
    `ack {seq: 1}`), and fans the seed out as an ordinary update. Otherwise it answers
    `seed_rejected` and does nothing.
  - **A rejected seed.** The client throws its document away and sends `doc_sync
    {after: 0}`. It never merges its own seed into the log: two seeds from the same JSON
    would double every paragraph.
  - **Readers** never seed. A reader on an empty log keeps the fetched JSON as K-A shows
    it, and binds to the document when the first update reaches them.
  - **Staleness of the JSON.** A seed is taken from the article as fetched when the page
    opened. If it was saved by someone else in between (a K-A fallback save, K32), the
    seeding client re-fetches once when it sees `synced {seq: 0}` and seeds from that.

- **K30 — Awareness is ephemeral, batched, and for writers.**
  - **The frame.** `0x03`, y-protocols' awareness update, which carries the sender's
    cursor, selection and `user: {name, color}`. The client sends its **full local
    state** each time, so only the newest frame of a connection matters.
  - **Not stored.** The hub relays it to the other subscriptions on this hub at once.
  - **Other hubs.** A hub does **not** `pg_notify` per frame: cursor movement would
    take Postgres's cluster-wide commit lock many times a second (L6, L13a). It
    coalesces the newest awareness frame per connection per doc, and every 250 ms sends
    **one** notification per doc on the presence publisher's connection (L7), holding
    the frames base64-encoded under the payload limit. A hub starts a new notification
    when the next frame would take it over 6 KiB; **a single frame over 4 KiB raw** (the
    limit above) is dropped and counted, since base64 would not fit.
    This revises the lead's 6 KiB: the *encoded* payload is what NOTIFY's 7 KiB cap
    (L6) bounds.
  - **Leaving.** The server does not decode awareness, so it cannot announce a
    removal. A closing socket sends nothing; the provider sends a removal update on
    unload as best effort; remote states expire after y-protocols' 30 s without renewal
    (the library renews each client's state every 15 s).
  - **Readers send none** (dropped, K26): the "who is here" avatars come from presence
    on `knowledge.article:<id>` (K13), as in K-A.
  - **Colour.** Stable per user id, from a fixed palette of twelve accessible colours
    chosen by hashing the id; a name is the directory's, from the article page's
    presence (the person's own name is in the session).

- **K31 — Compaction by a writer's browser.**
  - **When.** After a store, if the log is over 4 MiB or 2000 rows, the server sends
    `compact_requested {upTo: <the newest seq>}` to **that writer's connection**
    (the one that has just proved it holds a document), at most once a minute per doc.
  - **The client** answers only if it has applied every row up to `upTo`. It sends one
    `compact` frame: `upTo` and `Y.encodeStateAsUpdate(doc)` (the whole state, which may
    contain newer changes than `upTo`; replaying the rows after it is idempotent).
  - **The server**, in one transaction under the row lock, deletes the rows with
    `seq ≤ upTo` and inserts one at `seq = upTo` holding the data. It refuses
    (`compact_rejected`, nothing changed) when:
    - `upTo` is not greater than the newest compacted `seq`, or greater than the log's
      newest (**a late compaction for an older seq** is the first case);
    - the data exceeds 2 MiB;
    - the sender cannot write.
  - **Not fanned out.** The state is the same as the rows it replaces. A hub that
    could not fetch a removed row says `doc_stale` (K28).
  - **Why this is safe without a Yjs server.** Yjs state-based encoding of the whole
    document is by construction a superset of every update it has applied. The one way
    to lose data is a client that compacts before it applied a row it asked for, which
    the `upTo` condition prevents.

### Content, history and the interface

- **K32 — The JSON save in collaborative mode; the log is reset by a single-editor
  save.**
  - **The save.** Each writer's client debounces `PUT /knowledge/articles/{id}/content`
    **5 s after its document last changed (locally or by an update from another), and at
    most every 30 s**, with a random 0–2 s jitter, and skips a save when the
    `content.changed` of someone else's later save arrives first. Several clients may
    save the same state; the saves are harmless duplicates, and only a document that is
    *changed* needs one. A client flushes on leaving the page, best effort (K9's
    `keepalive`).
  - **`mode: "collab"`.** The body gains `mode: "collab"`, and `expectedVersion` is
    **not required** with it. The server takes the article's row lock, checks write
    access, the lock (`409 article_locked`) and the document (K8), saves, and **does not
    compare versions**: the version conflict is not shown to users. Version coalescing
    (per author, per 10 minutes) and link, mention and file bookkeeping are K9's. A
    `mode` that is absent is `single`, today's behaviour.
  - **What the JSON is.** It is the saving client's `editor.getJSON()`, validated by
    the allowlist. The server cannot check that it matches the log: a writer could save
    something else, as a writer could always. Whoever saves is recorded as author; a
    version may therefore be attributed to the person whose browser saved last rather
    than to the one who typed. This is accepted for phase 1 and stated in the user
    guide.
  - **The lag.** Search, the Assistant, backlinks, Markdown export and history show the
    JSON, so they trail the live text by at most 30 s plus the idle time.
  - **A single-editor save resets the log.** A save without `mode: "collab"` (the K-A
    fallback, K33), and the restore endpoint, **delete the article's log in the same
    transaction** and notify `doc_reset` on `knowledge.doc:<id>`. The JSON is then the
    truth again, and the next open seeds from it (K29). Without this, the next open
    would show the log and drop the fallback save.
  - **A client that hears `doc_reset`** discards its document, re-fetches the article
    and re-subscribes. If it held text that had not reached the log, it shows *"This
    page was replaced by a save made without the live connection"* and offers the lost
    text as the K-A local draft.
  - **Reset by hand.** `POST /knowledge/articles/{id}/collab/reset` (writers; the
    Assistant mode `never`) deletes the log and notifies `doc_reset`. It is the way out
    of a log that a client cannot apply (K35). Contract operation
    `resetKnowledgeArticleLog`.

- **K33 — What the page does with co-editing on.**
  - **On for every article.** There is no per-article switch.
  - **When it is active.** A writer's page is in collaborative mode while the status is
    `live` and the `knowledge.doc` subscription is `ok` and synced. Otherwise it is in
    **K-A mode exactly**: the version token, the conflict dialog, the *"Kari is editing"*
    banner, the local draft. A change of mode while the page is open happens only at a
    quiet moment (nothing unsaved): when the socket drops while editing, the editor
    keeps its Yjs text, keeps typing locally, saves through the K-A path over the last
    version it knows (a `409` shows the K-A conflict dialog), and on reconnect resyncs
    (K28) and returns to collaborative mode.
  - **In collaborative mode** the conflict dialog, the banner and the local draft are
    **replaced** by live cursors with names and colours (K30), the save indicator of K9,
    and the avatars from presence. The undo history is Yjs's (`Collaboration` brings its
    own; StarterKit's `undoRedo` is turned off): a person undoes only their own changes.
  - **Readers** see the same document and cursors move (not theirs), in a read-only
    editor.
  - **The title** stays an ordinary field, saved with the content (K32); another
    person's title arrives with `meta.changed` and replaces the field unless the person
    has typed in it. Titles are last-writer-wins.
  - **Restoring a version** in collaborative mode does not call the restore endpoint
    (which resets the log, K32). The restorer's client loads the version, replaces the
    editor's content in **one transaction** (`setContent`), so everyone sees it as one
    change and can undo it, and saves through the collaborative save. The history lists
    it as a new version.
  - **Locking** an article (K11a) makes writers' frames read-only within a minute (K26)
    and the editor read-only at once on `meta.changed`.
  - **Offline.** No IndexedDB persistence in K-C. A page opened without the socket is K-A
    mode; durable offline editing is a later delivery (§13).

- **K34 — The browser: a provider in the shell, the assembly in the module.**
  - **The shell** (`packages/frontend-shell/src/live/`) gains binary support in
    `LiveClient`: `binaryType = "arraybuffer"`, `sendBinary(topic, kind, payload)`,
    and `listenDoc(topic, handlers)`, which shares the subscription with `listen`. The
    provider is React-free and tested with a fake socket (the `LiveSocket` interface
    gains `send(Uint8Array)` and binary `onmessage`). The shell itself does not import
    Yjs, so modules that do not edit documents pay nothing: the binary codec and the
    `DocChannel` interface (`onUpdate`, `onAwareness`, `onSynced`, `onAck`, …) are in the
    shell, the Yjs binding is in the module.
  - **The module** (`apps/knowledge/frontend/src/editor/collab/`) holds
    `CollabProvider`: a `Y.Doc`, an `Awareness`, the channel from the shell, the ack
    queue (K28), the seeding (K29), the compaction (K31) and the batching. It exposes
    `awareness` (the field `CollaborationCaret` reads from `provider.awareness`), and
    the editor is assembled with `Collaboration.configure({document})` and
    `CollaborationCaret.configure({provider, user})`.
  - **Batching.** `doc.on("update")` is collected for 100 ms, merged with
    `Y.mergeUpdates`, and sent as one frame, so a typist sends about ten frames a second.
    An update from a remote origin is never resent (the origin is the provider).
  - **Applying.** `Y.applyUpdate(doc, update, provider)` is inside a `try`: a thrown
    error is K35.
  - **Packages.** `yjs`, `y-protocols`, `lib0`, `@tiptap/y-tiptap`,
    `@tiptap/extension-collaboration` and `@tiptap/extension-collaboration-caret` join
    the workspace catalog, pinned to the lockfile's resolved versions (the catalog
    already carries the TipTap line at 3.32.2); their licences (all MIT) are listed in
    `reference/knowledge.md` with K18's.
  - **Block ids.** `UniqueID` (K18) assigns an id to a block that lacks one. Under Yjs
    two clients may assign different ids to the same block at once; the last write
    wins and the editor converges. K29's normalisation gives every seeded block an id
    first, so this happens only for blocks created concurrently.

- **K35 — When it breaks.**
  - **A log a client cannot apply** (a corrupt row; `Y.applyUpdate` throws). The client
    stops, shows *"The live copy of this page cannot be opened"*, and offers writers
    **Rebuild from the last saved version**, which calls `resetKnowledgeArticleLog`
    (K32). Readers are shown the JSON. The text typed since the last save (≤ 30 s) is
    lost. A counter, `vantigo.live.doc_apply_errors`, is reported by the client's
    `doc_error` frame (a text frame the hub counts and logs).
  - **The hard limit** (K27): a writer sees *"Live editing is paused for this page"*,
    the editor falls back to K-A mode, and the compaction request has already gone.
  - **The listener is down.** The socket is `degraded` (L7): K-A mode (K33). Writes
    already stored by this hub still reach its own subscribers; other hubs' do not,
    and `resync` follows the return (K28).
  - **A person's two tabs** are two connections and two Yjs clients; they converge like
    any two people. Their cursors show twice.

- **K36 — Telemetry and operations.**
  - **Counters:** `vantigo.live.doc_updates{result=stored|read_only|too_large|rate|log_full}`,
    `vantigo.live.doc_seeds{result}`, `vantigo.live.doc_compactions{result}`,
    `vantigo.live.doc_dropped{reason}`, `vantigo.live.doc_apply_errors`. A histogram,
    `vantigo.live.doc_replay_rows`.
  - **No new setting.** Co-editing follows `LIVE_ENABLED`. Without the socket every
    article is edited as in K-A, which is the operator's fallback.
  - **Proxies** pass binary frames as they pass text; the 75 s read timeout of L15 is
    enough. The admin page says that a proxy that limits the WebSocket message size
    must allow 4 MiB.
  - **Database.** `article_updates` is in the `knowledge` schema's grants (K-A already
    grants the schema). Its size is bounded by K27; the logs of a purged article go with it
    (cascade). A log is never needed for safety: a seed always rebuilds from the JSON, so
    deleting one (K32) is always safe.

## 5a. Decisions — delivery K-D, the other live screens

K-D does not wait for Knowledge: each screen is a small change on delivery 0's package,
and the layer lands after K-C only because it is stacked. It replaces polling with
topics, as L17–L19 did for Communications. Events carry identifiers only (L5): the
screen refetches through its ordinary request, which checks the person's permission.

- **K37 — The rule for every screen in K-D.**
  - **Topics** are keyless where the screen shows a list (a detail page filters by the
    event's id); one is keyed where the data belongs to one person.
  - **`Authorize` reuses the GET's permission** (the `x-vantigo-access` of its
    operation), through `live.HasPermission`. None of these screens' reads is
    row-level, so no key-level check is needed except the calendar's (K41).
  - **Notify on the transaction that writes**, once per changed aggregate (L6). A worker
    that holds a leased connection notifies on that connection (it is an `Execer`) and
    never asks the pool for a second (the lease workers' rule).
  - **Progress is rate-limited at the writer.** A state change always notifies. A
    progress write notifies at most **once a second per aggregate**, from the worker's
    own memory of when it last did; no platform helper is added until a third screen
    needs one.
  - **The fallback poll** keeps its interval but only while `useLiveTopic` returns
    `false` (status not `live`, or the subscription not `ok`), the way the inbox's
    does (L13). A `resync` refetches.
  - **A test per write path** that the topic is notified, as L18's, and a frontend test
    per screen that the poll is off while live and on otherwise.
  - **Host notifications are deferred.** A per-user topic (`user`) for the bell and the
    host's notifications needs a notifications design in the host (what is a
    notification, who may see it, how it is read and cleared); it waits for it (§13).

- **K38 — Customer imports.**
  - **Topic `customers.imports`**, authorised by `customers:import` and `customers:view`
    (the operation's rule). Event `import.changed`, id the import's id.
  - **Where `srv/customers` notifies:**

    | Area | Where |
    | --- | --- |
    | Created (upload) | `imports.go` `PostCustomersImports` → `InsertImport` |
    | Mapping saved (rows' results cleared) | `PutCustomersImportsByImportIdMapping` → `SetImportMapping` |
    | Check or run requested | `…Check`, `…Run` → `RequestImportCheck`, `RequestImportRun` |
    | Stop, revert to draft | `…Stop`: `RequestImportStop`, `RevertUnstartedImport`, `RecheckUnstartedRun` |
    | Deleted | `DeleteImport` |
    | The worker: started, progress, finished, failed | `import_worker.go`: `StartImport`, `UpdateImportProgress` (rate-limited), `FinishImport`, `FailImport` |
    | Swept after retention | `SweepImports`: one event without an id |

    The per-row `WriteImportRowOutcome` does **not** notify: the progress update that
    follows each chunk does.
  - **The screens.** The imports list and an import's page invalidate `["customers",
    "imports"]` (which includes the import's rows) on any event; the page ignores an id
    that is not its own. `IMPORT_POLL_MS` (2 s) becomes the fallback.

- **K39 — Energy imports.** As K38.
  - **Topic `energy.imports`**, authorised by `energy:import` and
    `energy:metering-points-view`. Event `import.changed`.
  - **Where `srv/energy` notifies:** `imports.go` (`InsertImport`, `SetImportMapping`,
    `RequestImportCheck`, `RequestImportRun`, `RequestImportAttach`, the stop and revert,
    `DeleteImport`), `import_worker.go` (`StartImport`, `UpdateImportProgress`,
    `FinishImport`, `FailImport`, `SweepImports`). `WriteImportRowOutcome`,
    `WriteImportRowAttach` and the spot-price and consumption importers' own writes
    (`spot_prices_import.go`, `consumption_import.go`) are not imports of this screen and
    do not notify.

- **K40 — Billing runs (invoices).**
  - **Topic `invoices.billing-runs`**, authorised by `invoices:access` (both GETs'
    rule). Event `run.changed`, id the run's id.
  - **Where `srv/invoices` notifies:**
    - the API: `InsertBillingRun` and its items (`POST /invoices/billing-runs`),
      `CancelBillingRun` with `CancelBillingRunItems`;
    - the worker (`billing_worker.go`): `StartBillingRun`, each item's
      `CompleteBillingRunItem`, `FailBillingRunItem` and `RetryBillingRunItem`
      (rate-limited per run, K37), `FinishBillingRun`, `FinishIdleBillingRuns`,
      `CancelAbandonedBillingRunItems`, `ReleaseBillingRunItem`.
  - **The screens.** `billing-runs.tsx` and `billing-run.tsx` invalidate the invoices
    runs' keys on an event whose id is theirs (the list on any). `BILLING_RUN_POLL_MS`
    (5 s) becomes the fallback. A run's items list is read through the same
    invalidation.

- **K41 — Calendar mailbox sync.**
  - **Topic `calendar.mailbox-sync:<userId>`**, a keyed topic: `Authorize` requires
    `calendar:access` and that the key **equals the caller's own id**; every other key is
    `forbidden`. The card is the caller's own (`GET /calendar/mailbox-sync` answers
    nobody else's). Event `sync.changed`, id the user's id. It is the first topic keyed
    by the person rather than by a thing, and so the precedent for a per-user topic.
  - **Where `srv/calendar` notifies:**
    - the API: `UpsertMailboxSync` (save), `NudgeMailboxSync` in the run handler;
    - the worker (`mailbox_worker.go`): `WriteMailboxSyncResult`,
      `SetMailboxSyncMailbox`, `MarkMailboxSyncNotified`, `MarkMailboxSyncConnected`.
  - **Not notified:** `nudgeMailboxes` called from an appointment's write
    (`mailbox_sync.go`), which makes many people's syncs due without changing what any
    card shows; and the claim and release rows (`ClaimDueMailboxSync`,
    `ReleaseMailboxSyncClaim`).
  - **The screen.** The card (`api/mailbox-sync.ts`) invalidates its key on an event.
    `WAITING_REFETCH_MS` (15 s) becomes the fallback.

- **K42 — What does not change.** Every other screen that polls (for example the
  reminder runs and the e-invoice transmissions) is not in K-D. It is added the same
  way when asked, as one more decision in this section. The Communications inbox is
  delivery 0's.

- **K43 — Documentation.** Each screen's user page says that its progress or state
  updates by itself, and that with the live channel unavailable it refreshes every few
  seconds as before; each module's reference page lists the topic, the permission and
  the notify points; `contributing/platform-packages.md` lists the topics in its table.

## 6. Permissions summary

| Request | Checked against |
| --- | --- |
| Open `/api/v1/live` | A valid session (L2) |
| Subscribe to a topic | The topic's `Authorize` (L9) |
| List the tree, search, read an article | `knowledge:articles` and the article's read access (K4–K6) |
| Edit content, title, icon, lock, trash, files | `knowledge:articles` and write access |
| Change an article's access or `ai_hidden` | Write access, or `knowledge:manage` on non-private articles |
| Restore another person's trash, empty the trash | `knowledge:manage` |
| Subscribe to `knowledge.doc:<id>`, receive the log | The article's read access (K26) |
| Have an update, seed, compaction or awareness frame stored or relayed | Write access, and the page not locked or trashed (K26) |
| Reset an article's log | Write access (K32) |
| Subscribe to a K-D topic | The permission of the screen's GET (K37–K41) |

## 7. Data model (schema `knowledge`, K-A)

| Table | Holds |
| --- | --- |
| `articles` | K3's columns. Indexes: `(parent_id, position)` where not trashed, GIN on `search` |
| `article_members` | `(article_id, user_id) → permission` |
| `article_access_default` | Materialised `(article_id) → permission, ai_hidden` (K5, K16) |
| `article_access` | Materialised `(article_id, user_id) → permission` |
| `article_versions` | K9 |
| `favourites` | K11 |

K-B adds `article_files` and `article_links`. K-C adds `article_updates`, in `00140_knowledge_coediting`. Delivery 0 adds
no table. Migration numbers: K-A claims `00135_knowledge_baseline`, K-B
`00136_knowledge_files_links` and K-C `00140_knowledge_coediting`. Main is at `00139`.
They are renumbered right before merging if main takes them.

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

**Co-editing (K-C):**

- **The relay (Go, against the test database):** a joiner is sent the log in order and
  then `synced`; an update is stored, acknowledged, fanned out and never echoed; a
  reader's update is dropped and counted; limits (256 KiB, rate, `log_full`); **the seed
  race** (two seeds at once: exactly one accepted, the log holds one row); **compaction**
  (replaces rows up to `upTo`, a late one is refused, an oversized one is refused);
  fan-out across **two hubs** (the origin hub skips its own notification, the other
  fetches, a removed row gives `doc_stale`); `doc_reset` after a single-editor save and
  after the manual reset; a lock or lost write access stops storing within the re-check.
- **The collaborative save:** `mode: "collab"` skips the version check, honours the lock
  and the allowlist, and coalesces versions.
- **The provider (vitest, fake socket):** frame codec, batching, the ack queue and its
  resend after a reconnect, seeding and a rejected seed, compaction on request, the
  fallback to K-A mode.
- **Convergence (vitest):** two `Y.Doc`s through a fake server converge after
  interleaved, offline and duplicated updates, and after a compaction in the middle.
- **A real-browser check:** below.

**The browser check:** two browsers, two people. One edits while the other watches, the
presence shows, and the inbox updates when a mail arrives. For K-C: both type in the same
paragraph and cursors show with names and colours; a third opens the page while the
others type and sees the whole text once; the connection is cut and restored mid-typing;
a locked page turns read-only.

**K-D:** a test per notify point; the screens' poll is off while live; a customer import
progresses on screen with the server's listener up, and polls again when it is down.

## 10a. Edge cases of co-editing

- **Two people open a never-collaborated page at once.** One seed wins; the other
  discards its document (K29).
- **A person saves in K-A mode while others co-edit.** The log is reset and the others are
  told (K32); the text the others had not yet saved to the log is offered back as a draft.
- **The tab is closed with text not yet in the JSON.** The text is in the log, so the next
  opener sees it; the JSON catches up when any writer's client next saves (K32).
- **Nobody with write access ever reopens the page.** The JSON stays behind the log by up
  to the last unsaved stretch. A reader opening it sees the log (K28); search and the
  Assistant see the JSON.
- **A reader whose access is revoked** is unsubscribed within 60 s (L11) and receives
  no further frames.
- **The log is huge.** Compaction is demanded at 4 MiB or 2000 rows and updates stop at twice
  that (K27).

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
| Per-user notification topic (`user`), the host's notifications | A notifications design in the host (K37) |
| Offline editing (IndexedDB persistence of the Yjs document) | K-C in use |
| Yjs inside Go (`yrs` under `wazero`), server-side compaction | Compaction proving fragile (K24, K35) |
| Titles and properties inside the Yjs document | K-C in use; titles are last-writer-wins until then (K33) |

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

### Revision 5 — K-C and K-D designed, 2026-11-08

- **§5 rewritten (K24–K36),** replacing the sketch:
  - the server is an **opaque relay**: no Yjs in Go, and `yrs` under `wazero` is rejected
    for phase 1;
  - **binary frames** (K25) with a defined layout and JSON control frames; the 1003
    close for binary goes, and the read limit rises;
  - **`live.DocStore`** (K26), so `srv/live` stays free of any table;
  - the **seed** rule (K29): one winner, under the article's row lock, so two openers
    cannot double the content;
  - **compaction** by a writer's browser, refused when late (K31);
  - the **JSON save** keeps search and history, with `mode: "collab"` skipping the
    version check, and a single-editor save **resetting the log** (K32), which the
    sketch had not thought of: without it a fallback save would be lost at the next
    open;
  - **awareness is batched per hub** and capped at 4 KiB raw (K30), because NOTIFY
    carries text (base64) under 7 KiB and per-frame notifies would hit the cluster-wide
    commit lock;
  - **acks and replay** (K28), since the server holds no state vector.
- **§5a added (K37–K43):** imports (customers, energy), billing runs and the calendar
  mailbox sync move to topics; host notifications are deferred.
- **§7, §13:** migration `00140`; offline editing, Yjs in Go and titles in the document
  are listed as later.
