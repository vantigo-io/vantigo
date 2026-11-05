---
title: "Frontend architecture"
description: "Workspace layout, module manifests, API client generation, i18n, tour targets for the Assistant, and UI conventions."
sources:
  - packages/frontend-shell/src/assist-targets.ts
  - packages/frontend-shell/src/ui
  - apps/host/frontend/src/lib/assist-targets.ts
  - apps/host/frontend/src/api/session-handlers.ts
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
  frontend-shell/              # @vantigo/frontend-shell — the shell's layout, app switcher, spotlight and i18n catalog registry
```

## Module manifests (`@vantigo/module-registry`)

Every module under `modules/<name>/frontend` exports a `FrontendModuleManifest` (defined in
`packages/module-registry/src/index.ts`) and registers no global state of its own. The host
shell imports the manifests and filters them against the enabled modules reported by
`GET /api/v1/system/modules` and the signed-in user's permission set.

A manifest declares:

- **`id`** — module identifier matching the backend module name (`customers`, `projects`, …).
- **`navGroups` / `navItems`** — sidebar navigation entries with `requiredPermissions` gates.
- **`routes`** — React Router route definitions mounted inside the authenticated shell.
  Heavy route views should use `React.lazy(...)` so each module splits into on-demand chunks,
  and every top-level module route is wrapped in `<ModuleErrorBoundary>` by the host shell.
- **`settingsPanels` / `personalPanels`** — sections contributed to `/settings` and `/profile`.
- **`quickCreateActions`** — global "New …" actions surfaced in the header and command palette.
- **`translations`** — `en` and `nb` translation trees registered under the module's i18next
  namespace.

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
