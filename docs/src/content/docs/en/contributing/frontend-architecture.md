---
title: "Frontend architecture"
description: "Workspace layout, module manifests, API client generation, i18n, and UI conventions."
---

Vantigo's frontend is a Bun workspace monorepo of React 19, TypeScript, Vite, and Mantine
packages composed into one single-page application by the host shell (`apps/host/frontend`).
Just as backend modules own their schema and HTTP routes, each frontend module package owns
its routes, navigation items, settings panels, command-palette actions, and translations.

## Workspace layout

```text
apps/
  host/frontend/               # Application shell, router, auth session, command palette
modules/
  customers/frontend/          # @vantigo/customers-frontend
  products/frontend/           # @vantigo/products-frontend
  energy/frontend/             # @vantigo/energy-frontend
  communications/frontend/     # @vantigo/communications-frontend
  projects/frontend/           # @vantigo/projects-frontend
  time/frontend/               # @vantigo/time-frontend
  expenses/frontend/           # @vantigo/expenses-frontend
  invoices/frontend/           # @vantigo/invoices-frontend
  staff/frontend/              # @vantigo/staff-frontend
packages/
  api-client/                  # @vantigo/api-client — generated OpenAPI client + CSRF/ETag fetch wrapper
  module-registry/             # @vantigo/module-registry — FrontendModuleManifest contract
  ui/                          # @vantigo/ui — shared Mantine theme, layout primitives, error boundary
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
