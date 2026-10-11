# @vantigo/staff-ui

The frontend of the `staff` module: the API layer, the i18n catalog, the shared
helpers and the pages the host composes into its routes.

## Layout

| Path | What lives there |
|---|---|
| `src/api/request.ts` | The module's fetch client, built on `@vantigo/frontend-api-client`; `json()` adds `If-Match` to a revision-guarded replace; the one query-key prefix every read uses. |
| `src/api/meta.ts` | `GET /meta` — the caller's capabilities, the business zone and its today, and the structure for `staff:view`. Nothing in this package re-derives a rule. |
| `src/api/settings.ts`, `src/api/structure.ts` | The A1 settings, and the four structure tables (list, add, replace, archive). |
| `src/api/people.ts`, `src/api/employments.ts` | The directory, one person and everything on their page — the replace, next of kin, roles, the self-service link and its user picker, the org view, the history, the sealed fields' set and reveal; employments, their terms chain, end and reopen, the § 14-6 check. |
| `src/api/me.ts` | My page: `GET /me`, `PUT /me` and `GET /me/events`. |
| `src/api-schema.d.ts` | Generated from `openapi/staff.yaml` by `bun run gen:client` — do not edit. |
| `src/lib/routes.ts`, `src/lib/navigation.ts` | The app's paths, and the sidebar destinations the host registers with the permission that offers each. |
| `src/lib/search.ts` | `validatePeopleSearch` — the host route's `validateSearch` for People. |
| `src/lib/terms.ts`, `src/lib/terms-form.ts` | The enumerations, the appointment forms per type, the hours rule per scheme, and a terms row as a form holds and sends it. |
| `src/lib/person-form.ts`, `src/lib/employee-number.ts` | A person as a form holds and sends it; the employee number's bounds. |
| `src/lib/errors.ts`, `src/lib/dates.ts`, `src/lib/format.ts` | What a refusal says and which field it lands on — a 409 rule's too; calendar dates in UTC. |
| `src/lib/events.ts` | A history entry's kinds and fields, and its payload as lines a person reads. |
| `src/components/` | `PersonPicker` (a person from `GET /people?q=`), `UserPicker` (a user from `GET /assignable-users?q=`), `PersonStatusBadge`, `FieldGroup` and `Fields` (a §5 group as a labelled section), `SealedField` (on file, reveal for 30 s, set), `TermsTimeline`, `ContractCheck`. |
| `src/pages/` | `people.tsx`, `person.tsx`, `my-page.tsx`, `settings.tsx`, and the `-`-prefixed pieces they compose: New person's two steps, the person and terms fields, the person page's tabs and forms, the structure tables and their forms. |
| `src/test/` | Test-only: the fetch stub, an in-memory fake of the endpoints the pages use (shaped per audience), fixtures, the route tree the host's routes stand in for, helpers and the vitest setup. |
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
