# @vantigo/planning-ui

The frontend of the `planning` module: the API layer, the i18n catalog, the shared
helpers and the pages the host composes into its routes — the plan (the board), a
project's plan, a person's timeline, the unbooked list, My planning and the settings —
and `PersonTimeline`, which the host also puts on Staff's person page.

## Layout

| Path | What lives there |
|---|---|
| `src/api/request.ts` | The module's fetch client, built on `@vantigo/frontend-api-client`; `json()` adds `If-Match` to a revision-guarded replace; `send()` keeps a refusal's code on a field error; the one query-key prefix every read uses (`PLANNING_QUERY_KEY`). |
| `src/api/meta.ts` | `GET /meta` — `canView`, `canManage`, `hasPerson`, `workforceVisible`, the settings for `manage`, the load units. Nothing in this package re-derives a rule. |
| `src/api/board.ts`, `plan.ts`, `timeline.ts`, `unbooked.ts` | The four views: people (or projects) against weeks, a project's plan, a person's forward view, the unbooked list. |
| `src/api/allocations.ts` | The rows: list, one, create, replace with `If-Match`, delete, confirm; each write answers the row and its warnings. |
| `src/api/projects.ts`, `people.ts`, `me.ts`, `settings.ts`, `events.ts`, `stats.ts` | The pickers' lists, My planning, the settings, the record, the dashboard's figures and attention items. |
| `src/api-schema.d.ts` | Generated from `openapi/planning.yaml` by `bun run gen:client` — do not edit. |
| `src/lib/routes.ts`, `src/lib/navigation.ts` | The app's paths, and the sidebar destinations the host registers with the permission that offers each. |
| `src/lib/search.ts` | `validateBoardSearch` and `validateWindowSearch` — the host routes' `validateSearch`. |
| `src/lib/weeks.ts`, `window.ts`, `load.ts`, `format.ts` | Civil-date and week arithmetic in UTC with the business zone's today; the default windows; the load colour scale; the package's figures and names in the reader's locale. |
| `src/components/` | `AllocationSheet` (the drawer that creates, changes, confirms and deletes a row), `PersonPicker`, `ProjectPicker`, `LoadCell`, `WeekHeader`, `AllocationBars`, `PersonTimeline`. |
| `src/pages/` | `board.tsx`, `project-plan.tsx`, `person-timeline.tsx`, `unbooked.tsx`, `me.tsx`, `settings.tsx`. |
| `src/test/` | Test-only: the fetch stub, an in-memory fake of the planning API shaped per audience, fixtures, the route tree the host's routes stand in for, helpers and the vitest setup. |
| `src/i18n.ts` | The `planning` catalog in `en` and `nb`, parity-tested by `src/i18n.test.ts`. |
| `src/index.ts` | The package entry point the host imports. |

## Boundaries

A module package never imports another module package or the host app;
`eslint.config.js` enforces that with `no-restricted-imports`, and every other
module package's list names this one. The module reads Staff and Projects through
the server's contracts, never through their packages: its pickers ask
`/api/v1/planning/people` and `/api/v1/planning/projects`.

Permissions are the host's answer, not the package's: the host guards the routes,
and what a page offers comes from `GET /meta`'s capabilities. Nothing here reads
`/api/v1/identity/access/me`.

## Commands

Run from the repository root:

```sh
mise exec -- bun run --cwd apps/planning/frontend test
mise exec -- bun run --cwd apps/planning/frontend typecheck
mise exec -- bun run --cwd apps/planning/frontend lint
mise exec -- bun run gen:client         # regenerates src/api-schema.d.ts
mise exec -- bun run translations:check # every string in both en and nb
```
