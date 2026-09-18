# @vantigo/time-ui

The frontend of the `time` module: the API layer, the i18n catalog, the shared
presentation helpers and the pages the host composes into its routes.

## Layout

| Path | What lives there |
|---|---|
| `src/api/request.ts` | The module's fetch client, built on `@vantigo/frontend-api-client`. |
| `src/api/entries.ts` | Time entries: list/detail reads, create, replace, delete and single-entry submission. |
| `src/api/weeks.ts` | The caller's own week (rows × seven days, totals) and submitting it. |
| `src/api/approvals.ts`, `src/api/people.ts`, `src/api/rates.ts`, `src/api/settings.ts`, `src/api/stats.ts` | The approval queue and its transitions, the people overview, rate cards, the lock date, and the dashboard and project hours. |
| `src/api/projects.ts` | Cross-module reads over HTTP with locally declared types: the caller's projects, a project's active billing lines, the caller's open tasks. |
| `src/api-schema.d.ts` | Generated from `openapi/time.yaml` by `bun run gen:client` — do not edit. |
| `src/lib/status.ts` | The entry statuses: the values the API answers, their i18n keys and colours. |
| `src/lib/week.ts` | Plain calendar dates (`mondayOf`, `weekDays`, `today`), reading and writing hours (`parseHours`, `formatHours`, `hoursBetween`) and the page URLs. |
| `src/components/` | Shared presentation: `EntryStatusBadge` and the week grid's `HoursCell`. |
| `src/pages/` | The pages the host mounts — `my-week.tsx`, `day.tsx` — and the `-`-prefixed row picker and entry form they compose. |
| `src/test/` | Test-only providers: `render.tsx`, the fetch stub, fixtures, the stand-in route tree and the vitest setup. |
| `src/i18n.ts` | The `time` catalog in `en` and `nb`. |
| `src/index.ts` | The package entry point the host imports. |

## Boundaries

A module package never imports another module package or the host app; `eslint.config.js`
enforces that with `no-restricted-imports`. What this module needs from projects it
reads over the projects HTTP API with types declared here, the way
`apps/projects/frontend/src/api/customers.ts` reads customers.

Permissions are the host's answer, not the package's: the pages here only ever show
the caller's own time, and what an entry allows comes from its `capabilities`.
Nothing here reads `/api/v1/identity/access/me`.

## Commands

Run from the repository root:

```sh
mise exec -- bun run --cwd apps/time/frontend test
mise exec -- bun run --cwd apps/time/frontend typecheck
mise exec -- bun run --cwd apps/time/frontend lint
mise exec -- bun run gen:client        # regenerates src/api-schema.d.ts
mise exec -- bun run translations:check # every string in both en and nb
```
