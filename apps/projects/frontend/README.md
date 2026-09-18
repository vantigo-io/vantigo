# @vantigo/projects-ui

The frontend of the `projects` module: the API layer, the i18n catalog, the shared
presentation helpers, and (from Task 12 onwards) the pages the host composes.

## Layout

| Path | What lives there |
|---|---|
| `src/api/request.ts` | The module's fetch client, built on `@vantigo/frontend-api-client`. |
| `src/api/projects.ts` | Projects list/detail/stats/timeline reads and the project writes. |
| `src/api/people.ts` | Project roles and the assignable-user search. |
| `src/api/lines.ts` | Billing lines. |
| `src/api/customers.ts`, `src/api/products.ts` | Cross-module reads over HTTP with locally declared types. |
| `src/api-schema.d.ts` | Generated from `openapi/projects.yaml` by `bun run gen:client` — do not edit. |
| `src/lib/` | Pure presentation helpers (status colours, enum → i18n key). |
| `src/i18n.ts` | The `projects` catalog in `en` and `nb`. |
| `src/index.ts` | The package entry point the host imports. |

## Boundaries

A module package never imports another module package or the host app; `eslint.config.js`
enforces that with `no-restricted-imports`. What this module needs from customers and
products it reads over their public HTTP API with types declared here, the way
`apps/energy/frontend/src/api/customers.ts` does.

## Commands

Run from the repository root:

```sh
mise exec -- bun run --cwd apps/projects/frontend test
mise exec -- bun run --cwd apps/projects/frontend typecheck
mise exec -- bun run --cwd apps/projects/frontend lint
mise exec -- bun run gen:client        # regenerates src/api-schema.d.ts
mise exec -- bun run translations:check # every string in both en and nb
```
