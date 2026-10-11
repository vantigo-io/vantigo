# @vantigo/staff-ui

The frontend of the `staff` module: the API layer, the i18n catalog, the shared
helpers and the pages the host composes into its routes.

## Layout

| Path | What lives there |
|---|---|
| `src/api/request.ts` | The module's fetch client, built on `@vantigo/frontend-api-client`; `json()` adds `If-Match` to a revision-guarded replace; the one query-key prefix every read uses. |
| `src/api/meta.ts` | `GET /meta` — the caller's capabilities, the business zone and its today, and the structure for `staff:view`. Nothing in this package re-derives a rule. |
| `src/api/settings.ts`, `src/api/structure.ts` | The A1 settings, and the four structure tables (list, add, replace, archive). |
| `src/api/people.ts`, `src/api/employments.ts` | The directory, one person, the create; an employment with its first terms. |
| `src/api-schema.d.ts` | Generated from `openapi/staff.yaml` by `bun run gen:client` — do not edit. |
| `src/lib/routes.ts`, `src/lib/navigation.ts` | The app's paths, and the sidebar destinations the host registers with the permission that offers each. |
| `src/lib/search.ts` | `validatePeopleSearch` — the host route's `validateSearch` for People. |
| `src/lib/terms.ts`, `src/lib/terms-form.ts` | The enumerations, the appointment forms per type, the hours rule per scheme, and a terms row as a form holds and sends it. |
| `src/lib/person-form.ts`, `src/lib/employee-number.ts` | A person as a form holds and sends it; the employee number's bounds. |
| `src/lib/errors.ts`, `src/lib/dates.ts`, `src/lib/format.ts` | What a refusal says and which field it lands on; calendar dates in UTC. |
| `src/components/` | `PersonPicker` (a person from `GET /people?q=`), `PersonStatusBadge`. |
| `src/pages/` | `people.tsx`, `settings.tsx`, and the `-`-prefixed pieces they compose: New person's two steps, the person and terms fields, the structure tables and their forms. |
| `src/test/` | Test-only: the fetch stub, an in-memory fake of the endpoints the pages use, fixtures, the stand-in route tree and the vitest setup. |
| `src/i18n.ts` | The `staff` catalog in `en` and `nb`, parity-tested by `src/i18n.test.ts`. |
| `src/index.ts` | The package entry point the host imports. |

## Boundaries

A module package never imports another module package or the host app;
`eslint.config.js` enforces that with `no-restricted-imports`, and every other
module package's list names this one. The module depends on identity alone.

Permissions are the host's answer, not the package's: the host guards the routes,
and what a page offers comes from `GET /meta`'s capabilities. Nothing here reads
`/api/v1/identity/access/me`.

## Commands

Run from the repository root:

```sh
mise exec -- bun run --cwd apps/staff/frontend test
mise exec -- bun run --cwd apps/staff/frontend typecheck
mise exec -- bun run --cwd apps/staff/frontend lint
mise exec -- bun run gen:client         # regenerates src/api-schema.d.ts
mise exec -- bun run translations:check # every string in both en and nb
```
