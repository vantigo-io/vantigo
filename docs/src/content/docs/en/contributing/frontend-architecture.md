---
title: "Frontend architecture"
description: "Workspace layout, how a module reaches the host, the side panel host and how a module adds a panel, API client generation, i18n, tour targets for the Assistant, and UI conventions."
sources:
  - packages/frontend-shell/src/assist-targets.ts
  - packages/frontend-shell/src/side-panel.tsx
  - apps/host/frontend/src/routes/__root.tsx
  - apps/communications/frontend/src/hooks/useConversationsShell.tsx
  - packages/frontend-shell/src/ui
  - packages/frontend-shell/src/live
  - apps/host/frontend/src/lib/assist-targets.ts
  - apps/host/frontend/src/api/session-handlers.ts
  - apps/host/frontend/vite.config.ts
  - tools/assist
---

Vantigo's frontend is a Bun workspace monorepo of React 19, TypeScript, Vite, and Mantine
packages composed into one single-page application by the host shell (`apps/host/frontend`).
Just as backend modules own their schema and HTTP routes, each frontend module package owns
its routes, navigation items, settings panels, command-palette actions, and translations.

## Workspace layout

```text
apps/
  host/frontend/               # @vantigo/app — the application shell, router, auth session, command palette
  customers/frontend/          # @vantigo/customers-ui
  products/frontend/           # @vantigo/products-ui
  energy/frontend/             # @vantigo/energy-ui
  communications/frontend/     # @vantigo/communications-ui
  projects/frontend/           # @vantigo/projects-ui
  time/frontend/               # @vantigo/time-ui
  expenses/frontend/           # @vantigo/expenses-ui
  invoices/frontend/           # @vantigo/invoices-ui
  staff/frontend/              # @vantigo/staff-ui
  calendar/frontend/           # @vantigo/calendar-ui
  entra/frontend/              # @vantigo/entra-ui — Microsoft Entra ID's wizard
packages/
  frontend-api-client/         # @vantigo/frontend-api-client — the fetch client every module's API calls go through
  frontend-shell/              # @vantigo/frontend-shell — the shell's layout, side panel host, app switcher, spotlight and i18n catalog registry
```

## How a module reaches the host

There is no manifest package. A module frontend (`apps/<name>/frontend`, `@vantigo/<name>-ui`) is a
plain package whose `package.json` `exports` name what the host may import: `.` for its `index.ts`
(components, query options, hooks), `./i18n` for its catalog, and `./pages/*`, `./components/*`,
`./api/*` for the files the host imports by path. The host (`apps/host/frontend`) does the wiring:

- **Routes.** File-based TanStack Router routes under `src/routes/<module>/`, each a few lines that
  render the module's page and validate the URL's search parameters.
- **Navigation and apps.** `src/apps.ts` lists the apps, their sidebar items and the permissions that
  unlock them; `src/navigation.ts` filters them against the enabled modules (`moduleKeys`) and the
  signed-in person's permissions (`hasPermissions`).
- **Translations.** `src/i18n.ts` imports each module's `./i18n`, which registers the module's `en`
  and `nb` catalog.
- **The layout.** `src/routes/__root.tsx` renders `AppShellLayout` from `@vantigo/frontend-shell`,
  with the header actions, the sidebar, Spotlight and the side panel.

Modules never import each other. What one module needs of another goes through the host or through a
`contracts` slot on the server.

## The side panel (`SidePanelHost`)

The shell has one right-hand panel, `AppShellLayout`'s `aside`, and `packages/frontend-shell`
(`src/side-panel.tsx`) turns it into a host for several panels: today the Assistant
(`@vantigo/assistant-ui`) and Conversations (`@vantigo/communications-ui`).

- **The seam.** A panel is a `SidePanel`: `key`, `label`, `icon`, an optional `badge`, an optional
  `hidden` (listed, not shown now: the Assistant on its own page, Conversations in the Inbox) and
  `render()`. The layout owns the state with `useSidePanelController()` and gives the aside to the shell with
  `controller.aside(panels)`; `SidePanelHost` renders it. The controller holds whether the panel is open,
  the panel in front, the mode (`one` or `both`), the width, the divider's ratio and which panels have
  been mounted.
- **Behaviour the host owns.** One panel at a time under tabs, or from the `md` breakpoint both stacked
  with a divider (drag, or the arrow keys; `clampSplit` keeps 200 px for each); below `md` it is always
  one panel, and below `sm` the aside is the full-screen sheet. A panel is mounted the first time it is
  shown and then stays mounted, hidden with `display: none` and `inert` when another is in front, so
  its state survives navigation and switching. When the panel in front becomes `hidden`, the host
  closes. Width, mode, ratio and the last panel are remembered in `localStorage`
  (`vantigo.sidePanel.width|mode|ratio|last`) inside try/catch; the width is read once from the
  Assistant's old key `vantigo.assistant.panelWidth`.
- **What a panel gets.** Inside `render()`, `useSidePanelShown()` says whether the panel is open and in
  front (stop listening to live topics and timers when it is false) and `useSidePanelActions()` closes the panel or
  brings another forward. Everything else, including the width handle and the tabs, is the host's.

**Adding a panel.** (1) In the module package, export a panel component and a hook, like
`useConversationsShell`, that takes the `SidePanelController` and returns its `SidePanel` entry (`undefined` when
the person cannot use it), a header button, and a shortcut that calls `controller.toggle(key)`. (2) In
`__root.tsx`, call the hook with the module-on and permission checks the entry needs, put the button in
`headerActions` and the entry in the list given to `controller.aside([...])`. (3) Add the module's tabs strings
to its own catalog, and a test that renders the hook inside a `useSidePanelController()` harness
(see `ConversationsPanel.test.tsx`). Pick a shortcut that collides with neither the shell's (Cmd/Ctrl+J, Cmd/Ctrl+K)
nor the browsers'; the Conversations panel uses Cmd/Ctrl+Shift+U, which Edge's *Read aloud* shares on Windows.

## Generated API client (`@vantigo/api-client`)

Frontend packages never hand-write REST URLs or `Response` types. All HTTP calls go through
`@vantigo/api-client`:

1. Each module's OpenAPI 3.1 contract lives in `openapi/<module>.yaml` (sharing common
   components from `openapi/common.yaml`).
2. Running `bun run gen:client` bundles the specs into `openapi/dist/openapi.json` and runs
   `@hey-api/openapi-ts` to generate typed SDK functions and TypeScript interfaces in
   `packages/api-client/src/generated/`.
3. `packages/api-client/src/http.ts` wraps the generated client with:
   - Automatic `X-CSRF-Token` header injection on mutating requests (`POST`, `PUT`, `PATCH`, `DELETE`).
   - Optimistic concurrency (`If-Match` / `ETag`) helpers for revision-guarded resources.
   - Problem Details (`application/problem+json`) parsing into `ApiRequestError`, including
     field-level validation errors (`errors: Record<string, string[]>`).

## Session handling across module clients (`@vantigo/frontend-api-client`)

Each frontend package builds its own fetch client with `createApiClient` from
`@vantigo/frontend-api-client` in `src/api/request.ts`, and exports the client's
`request`, `setAuthStateClearer` and `setUnauthorizedHandler`. On a 401 a client calls its auth-state
clearer, then its unauthorized handler — unless the request passed `handleUnauthorized: false`.
A client whose callbacks were never set does neither, and the page only shows an error.

The host sets them for every client in one place,
[`apps/host/frontend/src/api/session-handlers.ts`](https://github.com/vantigo-io/vantigo/blob/main/apps/host/frontend/src/api/session-handlers.ts):
`main.tsx` calls `setSessionHandlers` once, which drops the cached session query and sends the person to
`/session-expired`. The host imports each client through the package's public
`./api/request` subpath; module packages never import each other. A new module package with its own client
is added to `sessionClients` there — the file's test fails while a package under `apps/` with a
`setUnauthorizedHandler` in `src/api/request.ts` is missing from it, and checks that a 401 from every
client reaches the host's handlers.

## Data fetching and state management

- **Server state** lives in **TanStack Query (`@tanstack/react-query`)**. Use hierarchical
  query keys (e.g. `['customers', 'detail', customerId]`) and invalidate related keys in
  mutation `onSuccess` callbacks.
- **URL state** (`useSearchParams`) is preferred for table filters, pagination cursors, active
  tabs, and modal targets so views are bookmarkable and survive refreshes.
- **Avoid manual `fetch` inside `useEffect`**. Always use `useQuery` or `useMutation` so
  cancellation, retries, caching, and loading states are handled consistently.

## Live screens

`@vantigo/frontend-shell` owns one WebSocket per tab, to `GET /api/v1/live?v=1`
([Live updates](/en/contributing/platform-packages/#live-updates)). The host mounts
`LiveProvider` once, under the signed-in layout and keyed on the session's user id, so the
sign-in and pay pages open no socket and signing in as someone else in the same tab
reconnects. The shell takes no TanStack Query dependency: a module's callback calls
`queryClient.invalidateQueries` itself. Vite's dev proxy has `ws: true`, and it presents the API's origin on a WebSocket upgrade. Browsers send no `Sec-Fetch-Site` on a WebSocket handshake, so the server judges it by its `Origin`, which must equal `APP_URL`'s.

- **`useLiveTopic(topic, onEvent, { enabled })`** subscribes while the component is mounted;
  two components on one topic share the subscription. `onEvent` receives
  `{ kind, ids }` or `{ kind: "resync" }` (tell them apart with `"ids" in event`). Calls are
  debounced per topic with a 250 ms trailing window, the distinct ids of that window
  gathered; `ids` is empty when the events carried none, as after a bulk change.
- **The contract of `onEvent`:** an event is a hint that something changed, never data. The
  callback invalidates the queries the screen shows and lets the ordinary, permission-checked
  request fetch them. On `resync` it must refetch **everything** the screen shows, because
  events may have been missed: after a reconnect, a slow-consumer drop, or the listener's
  return.
- **`useLiveTopic` returns a boolean:** `true` only while the socket is live **and** the
  server has answered `ok` for that topic. Each `ok` triggers one `resync` to that topic's
  subscribers, which covers the window between a screen's fetch and its subscription.
- **`useLiveStatus()`** is `connecting | live | degraded | unavailable`. After five failed
  attempts the status is `unavailable` and the provider tries again every 60 s; this covers
  `LIVE_ENABLED=0` and a proxy that refuses upgrades. A close with `4401` (the session
  ended) stops reconnecting; `4429` (too many sockets) makes the status `unavailable` with
  the 60 s retry; `1008` backs off. A connection counts as successful only after its first
  frame or 5 s, and no `pong` within 10 s of a ping reconnects.
- **Fallback polling.** A screen made live keeps its poll, used **only while its topic is
  not live**: `refetchInterval: live ? false : 60_000`, with `live` the boolean from
  `useLiveTopic`, decided per topic. Without the poll a screen would stay stale for as long
  as the socket is down. The Communications list and the open conversation each poll every
  60 s while not live. A staged attachment's scan status has no event, so it keeps its own
  2 s poll regardless.
- **`useLiveUserId()`** is the signed-in person's id the provider was mounted with, or `null`
  outside one: what a per-person topic is keyed by (`calendar.mailbox-sync:<id>`).
- **The other live screens.** The customers and energy imports, the invoices billing runs and
  the Outlook sync card are live the same way: a hook beside the query options
  (`useImportsLive`, `useBillingRunsLive`, `useMailboxSyncLive`) invalidates on an event and
  returns the boolean, and the query options take it (`importQueryOptions(id, live)`) and
  return no `refetchInterval` while it is `true`. A page for one import or run ignores an
  event whose id is another's; an event without an id and a `resync` refetch whatever is
  shown. Their fallback intervals are the ones they had: 2 s for an import, 5 s for a run, 15 s
  for the sync card.
- **`usePresence(topic, state)`** announces `viewing`, `editing` or `replying` (or none) on
  a topic whose server side allows presence, repeats it every 20 s, and returns the other
  members. It collapses a person's tabs to one member with the strongest state (editing,
  then replying, then viewing) and leaves the signed-in person out. A screen shows it as it
  fits; the Communications thread shows "Also here: …" and "… is replying …".

### Binary frames and documents

`LiveClient` also carries the binary frames of a document topic
([Platform packages](/en/contributing/platform-packages/#binary-frames-and-documents)): it
sets the socket's `binaryType` to `arraybuffer` and a screen that edits a document uses
`useLiveClient()` for the client. The shell never imports Yjs; the Yjs side is the module's.

- **`encodeFrame` / `decodeFrame`** code the frame: `kind` (1 byte), the topic's length (2 bytes,
  big endian), the topic, the payload. `KIND_UPDATE`, `KIND_SEED`, `KIND_AWARENESS` and
  `KIND_COMPACT` name the kinds. A binary frame that does not decode, has an unknown kind or is
  for a topic without a listener is ignored.
- **`client.listenDoc(topic, handlers)`** shares the topic's one subscription with `listen` and
  `useLiveTopic`. `onSubscribed` is called after every `ok` and every server `resync` (the document
  must send `doc_sync` again); `onUnsubscribed` when the connection or subscription is lost;
  `onBinary(kind, payload)` for update and awareness frames; `onControl(frame)` for the JSON
  frames of the document protocol (`synced`, `ack`, `update_rejected`, `seed_accepted`,
  `seed_rejected`, `compact_requested`, `compact_rejected`, `doc_reset`, `doc_stale`).
- **`client.sendBinary(topic, kind, payload)`** and **`client.sendJson(frame)`** send on the open
  socket and return `false` when it is not open. Nothing is queued: a document keeps what it
  could not send and sends it after `onSubscribed`.
- **Tests** use the fake socket of `live.test.tsx`; Knowledge's `src/test/collab-server.ts` is an
  in-memory server of the whole document protocol that a module's tests can reuse.

## Internationalisation (`en` and `nb`)

Every user-facing string is translated in both English (`en`) and Norwegian Bokmål (`nb`):

- Host translations live in `apps/host/frontend/src/i18n/en.json` and `nb.json`.
- Module translations live in `modules/<name>/frontend/src/i18n/en.json` and `nb.json`.
- Parity between `en` and `nb` keys (including `{{interpolation}}` placeholders) is enforced
  in CI by `bun run translations:check` (`tools/i18n/check-translations.ts`).

## Tour targets for the Assistant

The Assistant can walk a person through a screen with a guided tour ("Show me"). It points only at
controls a module has **registered**; it never writes a selector. To let it point at a control:

1. **Mark the control** with `data-assist="<module>.<screen>.<control>"` — for example
   `data-assist="energy.metering-points.new"`. Mantine's `Button`, `TextInput`, `Select` and the like pass a
   `data-*` prop to their element; wrap a composite picker in a `Box` that carries it. The value must be a plain
   string literal, never an expression. Mark one element per key per screen.
2. **Register it** in the package's `src/assist-targets.json`: `{key, route, label: {en, nb}, description:
   {en, nb}}`. `route` is the route pattern the control is on (`/energy/metering-points`, or
   `/customers/{customerId}` for a record's screen). `label` is the control's name in the interface, in both
   languages; `description` is one line saying what it is for — the model reads the English one.
3. **Export it.** A package with targets has `src/assist-targets.ts` (exporting `assistTargets`, typed
   `AssistTarget` from `@vantigo/frontend-shell`), re-exports it from its index and lists
   `"./assist-targets"` in its `package.json` `exports`; the host adds it to `src/lib/assist-targets.ts`. A first
   registry in a package also needs a copy of `src/assist-targets.test.ts` from another package.
4. **Regenerate the server's copy** with `go generate ./...` in `apps/server`, and commit
   `internal/assistant/assist-targets.json`. A Go test and CI fail while it is stale.
5. **Document it:** the module's reference page lists its keys.

A control inside a dialog exists only once the dialog is open, so a tour step can wait for it for three
seconds and then shows centred; the Assistant's prompt tells the model to say "press the button, then Next". If a
step ever has to point into an open `Select` or `Menu` (a portal), raise that dropdown's `zIndex` above the
tour's 10000 as [Mantine documents](https://mantine.dev/core/tour/). The test in each package
(`assistTargetProblems`) keeps keys and markers in step; renaming a control means renaming its key everywhere.

## Page surfaces

Every page sits on the shell's grey background, and its content sits on white, bordered
"paper": the `PageSection` component from `@vantigo/frontend-shell`. It is a Mantine `Card`
with a border, padding `lg` and radius `md`, the same in every app, and it takes its colours
from Mantine's tokens so it follows the colour scheme.

- **What goes on it.** The `PageHeader` and a row of `KpiCard`s stay outside, on the grey,
  above the sections. Everything else the page shows — a form, a detail block, a list with
  its filters — sits in a `PageSection`.
- **One surface per logical block.** A list page has one section holding the filters and
  the table together; a detail page has one section per block. Never put a section inside
  a section: small cards inside one (a list item, a tile) are plain Mantine `Card`s with a
  smaller padding.
- **Full-bleed content.** A table, grid or calendar that should run to the border takes
  `padding={0}`; the border and radius stay.
- **Header row.** `title` names the section (it renders a heading and makes the section a
  named region) and `actions` puts buttons on the right of it. A section without a title
  passes its children straight to the card, so `Card.Section` works inside it.
- **Colours.** Never hardcode a colour on a surface (`white`, `#fff`,
  `--mantine-color-white`): use `--mantine-color-body` and the other Mantine tokens, which
  dark mode redefines.

`KpiCard` and `WidgetCard` are the dashboard's own cards and keep their own look; dialogs,
drawers and the sign-in pages are not page surfaces either.

## Verification commands

Run these from the repository root before opening a pull request:

```bash
bun run dev                  # Start the Vite dev server (proxies /api to localhost:8080)
bun run gen:client           # Regenerate @vantigo/api-client from openapi/*.yaml
bun run translations:check   # Verify en/nb translation key & placeholder parity
bun run format:check         # Check formatting with Oxfmt
bun run test                 # Run Vitest unit & component tests across all workspaces
bun run build                # Typecheck (tsc -b) and build the production SPA bundle
```
