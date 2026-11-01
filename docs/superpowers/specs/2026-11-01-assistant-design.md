# The Assistant — design

**Status:** agreed with the owner on 2026-11-01. Deliveries 0 and A are briefed under
[`plans/2026-11-01-assistant-briefs/`](../plans/2026-11-01-assistant-briefs/README.md);
T, B and C get their own briefs once A has landed.

## What it is

Every person in Vantigo has an **Assistant** (nb **Assistent**). It answers "how do I…"
from the documentation and shows the way. It reads the data the person may read. In
later deliveries it also does what the person may do, one change at a time or in bulk.
It never does anything the person could not do in the interface themselves.

It has two surfaces over one store of conversations:

- **The Assistant app** at `/assistant`, laid out like Claude or Gemini: a sidebar of
  every conversation the person has had anywhere in Vantigo, with search, rename, pin and
  archive, and a conversation view that streams answers and lets the person resume any
  earlier conversation.
- **The panel** in the shell. It opens from a button in the header, with Cmd/Ctrl+J, or
  from Spotlight. It docks on the right and stays open while the person moves between
  apps. It knows which app, route and record the person is looking at, and shows that as
  a chip the person can remove.

## Readings (the code as of `319a8e73`)

- **The access rule is in the contract.** Every one of the ≈780 operations in
  `openapi/*.yaml` carries `x-vantigo-access`. `module/router.go` `wrap` enforces it:
  rate limit, then `Access.Check`, then `WithPrincipal` and `WithRequest`, then the
  handler. Row-level rules are written in each handler (for example
  `projects/authorize.go`). So whatever reaches the handler through the router is
  checked exactly as the interface's own calls are.
- **The merged contract is served at runtime.** `GET /api/openapi.json`
  (`module/compose.go`) answers the contracts of the enabled modules.
- **There is no service layer.** Handlers call sqlc directly. Logic can be reused only
  by calling the operation.
- **Rule 6** of module boundaries forbids calling another module's HTTP endpoints
  in-process.
- **The AI client is prompt-only.** `internal/ai` `Client.Complete(ctx, task,
  Request{Prompt})` is a single user message: no system message, no tools, no
  streaming, 30 s timeout. Only OpenAI is implemented. `AI_API_KEY` is required and
  `AI_BASE_URL` must be https.
- **AI tasks are declared by the module that performs them** (`Module.AITasks`,
  `contracts.AITask`). An Owner chooses the model per task under Workspace admin › AI
  models (identity `ai_task_models`).
- **There are no tokens and no service accounts**, only session cookies and SCIM.
  `contracts.AsUser` and `Access.UserHolds` let a worker check a user's permissions
  with no request.
- **The built-in User role is empty and cannot be edited** (`00002_identity_baseline.sql`,
  `identity/authorization.go` `systemRoleChanged`). Permissions only add up; nothing
  can deny one.
- **There is no SSE or WebSocket support.** The main `http.Server` has no
  `WriteTimeout`.
- **Long jobs report progress through state rows the interface polls**: the energy and
  customers imports and the invoices billing runs, each copied, not shared.
- **The documentation can serve as the assistant's knowledge.** It is markdown in
  `docs/src/content/docs/{en,nb}`. `user/` and `admin/` are task-shaped and bilingual.
  The built site is served at `/docs` from the binary.
- **Mantine 9.7.4 ships `Tour`** in `@mantine/core`. Its step targets are CSS
  selectors that may mount after the step activates.

## Decisions

### S1 — The name

The app is **Assistant** in English and **Assistent** in Norwegian bokmål. Its names:

| What | Name |
| --- | --- |
| module key | `assistant` |
| route | `/assistant` |
| API mount | `/api/v1/assistant/` |
| schema | `assistant` |
| AI task | `assistant.chat` |

There is no persona name.

### S2 — The assistant acts through the real API, as the person

Its tools are the contract's operations. A call goes **through the composed router**
with an *assistant principal*, so the access rule, MFA, rate limits, body caps,
row-level rules and every refusal behave exactly as they do for the interface. There is
no second permission model and no hand-written tool per feature.

This is a **sanctioned exception to rule 6**, and becomes a new numbered rule:

- The platform's `Operations` dispatcher may call any operation of the merged contract
  in-process, through the router, for the assistant principal only.
- Only the `assistant` module receives the dispatcher. depguard and `Compose` enforce
  that.
- No module learns the assistant exists. The coupling is to the public contract, which
  is the most stable surface Vantigo has.

### S3 — The assistant principal

- **The grant.** When a person sends a message, identity issues an *assistant grant*
  bound to their current session, the conversation and the run. It stores the user, the
  parent session, the run, `expires_at` and `revoked_at`.
- **How the dispatcher carries it.** The dispatcher attaches the grant to the
  in-process request's **context**, never to a header or a cookie. A network request
  cannot set a context value, so the grant cannot be presented from outside.
- **How `Access.Check` honours it.** The grant must be current, the parent session must
  still be valid, and the user must be active and not locked. The principal is the
  session's own: same user, same MFA state, plus
  `Principal.Via = {Kind: "assistant", ConversationID, RunID}`.
- **Its lifetime.** Signing out, or the session expiring, stops the assistant at its
  next step.
- **Audit.** Each module's audit records the person. The assistant's own action log
  records what it did on their behalf (S9).

### S4 — Which operations the assistant may call

Each operation gets an effective **assistant mode**: `auto` (call it without asking),
`confirm` (call it only after the person confirms, from delivery B), or `never`. Compose
derives it at boot from the merged contract and the catalog:

1. The operation's `x-vantigo-assistant: auto | confirm | never`, when present. The
   extension is optional and states exceptions only. The OpenAPI lint checks its values.
2. Otherwise **`never`** when any of these hold:
   - the access rule is `anonymous`, `scim` or `session`;
   - it is a `policy:` rule naming `Owner`, `OwnerManagement`, `AuthorizationManagement`
     or `SystemAdmin`;
   - any permission it names is `Sensitive` in the catalog — **until the Owner says
     otherwise (S16)**. Delivery 0 applies this rule unconditionally; delivery A moves
     it into the workspace's permission levels and keeps it as the dispatcher's
     fail-closed default;
   - it is a file upload or download (`multipart/*`, or a response that is not JSON);
   - it is the assistant's own API.
3. Otherwise `auto` for `GET`, and `confirm` for every other method. A `POST` that only
   reads, such as a search, is marked `auto` explicitly.

`never` operations are not in the catalog the model sees. The contract's mode is the
widest an operation can ever be; the workspace's permission levels (S16) can only
narrow it.

### S5 — The tool surface is small and filtered by permission

The model gets a handful of tools, not hundreds. This is also what local models need
(S7).

| Tool | Delivery | What it does |
| --- | --- | --- |
| `search_docs(query)` | A | Finds documentation sections in the person's language and returns their excerpts and site paths. |
| `search_operations(query)` | A | Finds operations by summary, tag and path among **those the person may call** (their access rule passes `UserHolds`, their mode is not `never`). Returns ids and summaries. |
| `describe_operation(id)` | A | Returns the parameters and the request and response schemas, compacted: no `$ref` chains, enums inline. |
| `call_operation(id, params, body)` | A for `auto`, B for `confirm` | Calls the operation through the dispatcher. Returns the status and a compacted body: lists truncated with a count, and a `more` cursor. |
| `navigate(route, label)` | A | Answers a link the panel shows as a button. It never moves the screen by itself. |
| `show_tour(steps)` | T | Starts a pointer tour over registered targets (S11). |
| `propose_bulk(plan)` | C | Proposes a bulk change (S13). |

**Results are untrusted data.** They are wrapped as data, never as instructions, and
the system prompt says so. A write needs the person's confirmation however the request
came about.

### S6 — The AI platform grows to chat

`internal/ai` gains a second method beside `Complete`. Its shape:

- `Client.Chat(ctx, task, ChatRequest{System, Messages, Tools, MaxTokens}, onDelta)`.
- Messages are typed `system`, `user`, `assistant` (text and tool calls) and `tool`
  (results).
- Tools are JSON Schema functions.
- The answer streams: `onDelta` receives text deltas, and the final `ChatResponse`
  carries the assistant message with its tool calls and the token counts.

Rules:

- **The provider is the OpenAI-compatible Chat Completions API with tools and
  streaming.** That one implementation serves OpenAI, Ollama, vLLM, LM Studio and Azure
  OpenAI's compatible endpoint. An Anthropic provider is a later file behind the same
  types.
- **The provider-neutral types stay neutral.** Nothing OpenAI-specific leaks into a
  module.
- **The same guard as `Complete`:** no redirects, `Deps.HTTPTransport`, typed errors.
- **The budget is per call and longer for chat:** `AI_TIMEOUT`, default 120 s, because
  local models are slow.

### S7 — Local models are a first-class deployment

The SaaS is to run local models (Ollama or similar). Self-hosters may bring their own
model or provider.

- **`AI_PROVIDER=openai-compatible`** (an alias of `openai` that reads better)
  together with `AI_BASE_URL` allows an **empty `AI_API_KEY`**.
- **`AI_BASE_URL` may be `http://` when the host is loopback, private or a single-label
  name** such as `ollama`. A public `http://` host is refused at boot, and so is a key
  sent over `http://`.
- **The prompt and tool results are sized for small context windows:**
  - results are compacted;
  - older tool results in a conversation are replaced by a one-line summary once the
    conversation passes a budget (`AI_CONTEXT_TOKENS`, default 16 000);
  - a run is capped at 12 tool calls before it must answer.
- **Whether the data leaves the installation is visible.** For a provider that is not
  local, Workspace admin › Assistant says so: "Questions and the data the assistant
  reads are sent to *provider*". The Owner turns the assistant on knowingly.

### S8 — Who has the assistant

**The workspace setting** (`assistant.settings.mode`) is Owner-only and has three
values:

| Value | Who has the assistant |
| --- | --- |
| **Enabled for all** (default) | every active user; per-person switches are ignored |
| **Per person** | only people whose own switch is on |
| **Disabled for all** | nobody, whatever their switches say |

**The per-person switch** (`assistant.person_settings.enabled`, default off) sits on
the user's page in Workspace admin › Users. The assistant module provides it as a slot
the host mounts.

**Availability needs four things together:**
1. the instance has AI configured (`Deps.AI` is not nil);
2. the module is enabled;
3. the mode allows the person;
4. the person's own access to Vantigo is current.

There is no `assistant:use` permission. The switch replaces it, because a permission in
the built-in User role could never be taken away (*Readings*).

**Administering it** (the mode, per-person switches, retention) is
`policy:OwnerManagement`, like the other workspace-tier settings.

**Grouping is deferred.** Switching the assistant on for a group (for example an Entra
group) in Per person mode is not part of v1.

### S9 — Conversations are private

- **Only the person sees their conversations.** No Owner view, no export, no admin
  endpoint.
- **What they did is still visible elsewhere:**
  - each module's audit records the person as the actor of every change;
  - the assistant's **action log** (operation, mode, HTTP status, who confirmed, when)
    is part of the person's own conversation;
  - from delivery B, an Owner can see a count per person and day, without content, for
    cost and abuse.
- **Retention:** conversations older than the workspace's retention (default **12
  months**, set by the Owner, 1–60) are deleted by a worker, which deletes whole
  conversations by their last activity.
- **Erasure:** when identity erases or anonymises a person, their conversations, grants
  and settings go with them.
- **A customer's GDPR erase does not search conversation text.** Retention is the
  answer, and the reference says so plainly. The owner's comparison: like a post-it
  under the monitor, it goes away eventually.

### S10 — The panel while browsing

- **Opening it:**
  - the **Assistant** button in the shell header (`headerActions`);
  - **Cmd/Ctrl+J**;
  - from Spotlight: a query with no good match offers *Ask the assistant: "…"*, which
    opens the panel with the question sent.

  The button is shown only when the assistant is available to the person (S8).
- **Layout:** a right-hand panel that **pushes** the page, resizable, its width
  remembered in this browser. It belongs to the shell layout, not the page, so it stays
  open across navigation between apps. On narrow screens it is a full-screen sheet.
  Whether it is open is not remembered; it starts closed.
- **Context:** the panel knows the current app, the route, the route's ids and the page
  title, and never reads the page's contents.
  - It shows them as a **chip** in the composer ("Viewing: Customer Ola Nordmann ✕").
  - The chip follows navigation, and the person can remove it for the next message.
  - Each user message stores the context it was sent with.
  - The record's label comes from the module through a small per-route
    `assistantContext` resolver in the module frontend. When there is none, the chip
    names the screen.
- **Continuity:** clicking the button opens the **last conversation the person had
  open**, remembered on the server so it follows them between devices. *New
  conversation* is the only way to start fresh. The panel header has the conversation's
  title, a recent-conversations menu, *New conversation* and *Open in Assistant*.
- **Origin:** every conversation records where it began (app and route), and the
  Assistant app's sidebar shows it ("Started in Energy").
- **Links:** records in answers are links that open in the app with the panel still
  open. *[Take me there]* buttons come from `navigate`.
- **A run continues on the server** when the panel is closed or the person navigates
  away. A badge on the Assistant button shows *working*, *done* or, from B, *waiting
  for you*.
- **Out of scope:** the assistant never fills in the form on screen. It creates or
  changes the record through the API, with a confirmation, then offers to open it.

### S11 — "Show me how" with Mantine Tour (delivery T)

- **Answers first.** Every how-to answer gives steps named as the interface names
  them, in the person's language, with a link to the documentation page and, where it
  helps, a *[Take me there]* button.
- **Then a tour.** `show_tour` starts a Mantine `Tour`. Each step is
  `{route, target, title, body}`. When a step's route differs, the panel navigates
  first, then the step activates.
- **Targets come only from a registry.** Each module frontend exports its `data-assist`
  targets: key, route, and a one-line description in en and nb. The registry is the
  tool's enum, so the model cannot invent a selector.
- **A test** checks that every registered key appears as a `data-assist` attribute in
  that package's source, so a renamed control fails CI instead of a tour.
- **A target missing on screen** (hidden by permissions, not yet loaded after 3 s)
  shows its step centred.
- Steps inside portal dropdowns raise the dropdown's `zIndex`, as Mantine documents.
- Modules gain markers gradually. A module with none still gets the text answer.

### S12 — Writes (delivery B)

- **A `confirm` operation never runs on the model's say-so.** The run pauses in
  `awaiting_confirmation` with a **confirmation card**: the operation's summary, what
  it changes (fields, and before and after where a `GET` can read it), and *Confirm* or
  *Cancel*.
- **The run is a persisted state machine:**
  `running → awaiting_confirmation → running → done | failed | stopped`.
  A confirmation may come minutes later, after a reload or from another device.
- **After an action succeeds, the panel invalidates that module's TanStack queries**,
  so the screen the person is on shows the change.

### S13 — Bulk changes (delivery C)

**A bulk plan** has three parts:
- a **selector**: an `auto` list operation and its filters, paged to the end;
- a **per-item operation template**: a `confirm` operation, with the item's id bound
  into its path or body;
- the **values**.

It runs in four steps:
1. **Preview.** The server resolves the selector and shows:
   - the count;
   - a sample of 10;
   - the change;
   - the items the person may not change, pre-checked with `UserHolds` and the
     operation's rule.

   This is a *preview*, not a dry run. Handlers write directly and some have side
   effects outside the transaction, so a generic rollback is not possible, and the
   interface does not claim one.
2. **Canary.** On *Start*, the first 5 items run for real and their results are shown.
3. **Run.** On *Continue*, the rest runs as a job in a worker, in chunks of 100. Each
   item goes through the dispatcher as the person. Progress counters show, the job can
   be stopped between chunks, and there is a list of failed items with each one's
   refusal.
4. **The job's state row** follows the imports' shape. Its runner becomes a shared
   platform helper instead of a fourth copy.

### S14 — Cost and limits

- **Per run:** at most 12 tool calls and 4 model turns without a user message.
- **Per person:** the router's rate limit for `assistant.chat` operations.
- **Accounting:** every model call's tokens are recorded on the message.
- **A daily token budget** per person, set by the Owner, comes with B, together with
  the Owner's count view (S9).

### S15 — The contract's quality is the assistant's quality

- **Summaries:** delivery 0 fills in a `summary` on every operation that lacks one
  (communications 30, identity 93, others as found). The OpenAPI lint then requires one.
- **operationIds stay as they are.** They are API.
- **Search** indexes summary, description, tag and path, never only the operationId.

### S16 — The Owner decides what the assistant may use

Added on 2026-11-01, after delivery 0 showed 427 of 780 operations `never`. Most of them
are `never` only because they sit behind a `Sensitive` permission, and in customers
alone that covers contacts, the timeline, legal identity and billing profiles.

**Workspace admin › Assistant** gains a section, *What the assistant may use*:

- **The sensitive data switch** has two values: *Not available* (the default) and *Can
  read*. It sets the **default level** of every permission the catalog marks
  `Sensitive`.
- **The permission table** lists the catalog of the enabled modules, grouped by module
  and category, with the *Sensitive* badge where the catalog has one. Each permission
  has a **level**:

  | Level | The assistant may |
  | --- | --- |
  | **Off** | call no operation whose rule names the permission |
  | **Read** | call its `auto` operations; its `confirm` operations become `never` |
  | **Read and change** | call it as the contract's mode says (`confirm` needs delivery B) |

  The defaults: a permission that is not sensitive is *Read and change*, and a
  sensitive permission takes the switch's value (*Off* or *Read*).
- **Overrides:** an Owner may set any permission's level. Only overrides are stored, so
  a new permission in a later release takes its default. Each row offers *Reset to
  default*.
- **Before delivery B,** *Read and change* is stored and shown, and the interface says
  that changes come in a later version.

**The effective mode of an operation for this workspace** starts from the contract's
mode (S4 without its `Sensitive` clause). Each permission the operation's rule names
can then narrow it:
- **Off** makes it `never`;
- **Read** turns `confirm` into `never`;
- **Read and change** leaves it as it is.

Operations behind `policy:ActiveAccount`, `policy:Business` or `session` name no
permission. Only the contract's mode applies to them.

**Rules**
- **The levels only narrow, never widen.** The person must still hold the permission
  themselves, and the router still checks every call (S2, S3).
- **The dispatcher fails closed.** It applies the levels through a gate the assistant
  module installs. Without a gate, a sensitive permission makes its operations `never`,
  which is delivery 0's behaviour.
- **A change takes effect at the next tool call.** It is cached with the settings'
  version.
- **Changes are audited** with before and after, like the mode (S8).
- **The data notice (S7) sits beside the switch.** It names where the data goes, so
  turning on *Can read* for a provider that is not local is a decision the Owner makes
  knowingly.

## Deliveries

| Delivery | Contents | Depends on |
| --- | --- | --- |
| **0 — Platform** | `ai.Chat` with tools and streaming (S6), local models (S7); the assistant grant and principal (S3); the `Operations` dispatcher and the modes (S2, S4); `x-vantigo-assistant` and the summary lint (S4, S15); the new boundary rule; docs | nothing |
| **A — The Assistant app (read)** | The `assistant` module: schema, settings and switches (S8), what the assistant may use (S16), conversations (S9), runs and SSE, `search_docs`, `search_operations`, `describe_operation`, `call_operation` for `auto`, `navigate` (S5); the `/assistant` app and the panel (S10); retention and erasure; docs in en and nb | 0 merged |
| **T — Tours** | `show_tour`, the target registry and its test, markers on the most-used screens (S11) | A merged |
| **B — Writes** | `confirm` with cards, the persisted run state machine, query invalidation, the daily budget, the Owner's count view (S12, S14) | A merged |
| **C — Bulk** | `propose_bulk`, preview, canary, worker job, shared job runner (S13) | B merged |

## Open questions

- **Q1:** a second provider (Anthropic) — when, and whether per task or per instance.
- **Q2:** switching the assistant on for groups in Per person mode (S8).
- **Q3:** attachments — "help me import this CSV" — and whether the assistant may drive
  the import pipelines.
- **Q4:** whether the SaaS's local model needs a GPU per tenant or a shared inference
  service. This is an operator concern and affects only `AI_BASE_URL`.
