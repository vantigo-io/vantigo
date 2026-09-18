# @vantigo/projects-ui

The frontend of the `projects` module: the API layer, the i18n catalog, the shared
presentation helpers and the pages the host composes into its routes.

## Layout

| Path | What lives there |
|---|---|
| `src/api/request.ts` | The module's fetch client, built on `@vantigo/frontend-api-client`. |
| `src/api/projects.ts` | Projects list/detail/stats/timeline reads and the project writes. |
| `src/api/people.ts` | Project roles and the assignable-user search. |
| `src/api/lines.ts` | Billing lines. |
| `src/api/customers.ts`, `src/api/products.ts` | Cross-module reads over HTTP with locally declared types. |
| `src/api-schema.d.ts` | Generated from `openapi/projects.yaml` by `bun run gen:client` — do not edit. |
| `src/lib/status.ts`, `src/lib/billing.ts`, `src/lib/roles.ts` | The enumerations: the values the API accepts, their i18n keys and badge colours. |
| `src/lib/dates.ts` | `useProjectDates`, formatting a project's plain calendar dates and ranges in UTC. |
| `src/lib/search.ts` | The debounce every searchable picker in the package waits. |
| `src/lib/use-code-suggestion.ts` | The create form's code suggestion, asked for only while the field is untouched. |
| `src/components/` | Shared presentation: the status badge, the customer picker, one read-only `Field`, and `CustomerProjectsPanel` (the customer page's tab). |
| `src/pages/` | The pages the host mounts — `projects.index.tsx` (list), `projects.$projectId.tsx` (header and overview), `project-people.tsx`, `project-billing.tsx` — and the `-`-prefixed modals and timeline they compose. |
| `src/test/` | Test-only providers: `render.tsx`, the fetch stub, the stand-in route tree and the vitest setup. |
| `src/i18n.ts` | The `projects` catalog in `en` and `nb`. |
| `src/index.ts` | The package entry point the host imports. |

## Boundaries

A module package never imports another module package or the host app; `eslint.config.js`
enforces that with `no-restricted-imports`. What this module needs from customers and
products it reads over their public HTTP API with types declared here, the way
`apps/energy/frontend/src/api/customers.ts` does.

Permissions are the host's answer, not the package's: a page that must know whether
the caller may create takes it as a prop (`ProjectsPage`, `CustomerProjectsPanel`).
Nothing here reads `/api/v1/identity/access/me`.

## Commands

Run from the repository root:

```sh
mise exec -- bun run --cwd apps/projects/frontend test
mise exec -- bun run --cwd apps/projects/frontend typecheck
mise exec -- bun run --cwd apps/projects/frontend lint
mise exec -- bun run gen:client        # regenerates src/api-schema.d.ts
mise exec -- bun run translations:check # every string in both en and nb
```
