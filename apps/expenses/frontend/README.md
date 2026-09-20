# @vantigo/expenses-ui

The frontend of the `expenses` module: the API layer, the i18n catalog, the
shared presentation helpers and the pages the host composes into its routes.

## Layout

| Path | What lives there |
|---|---|
| `src/api/request.ts` | The module's fetch client, built on `@vantigo/frontend-api-client`, plus the one query-key prefix every read uses. |
| `src/api/meta.ts` | `GET /meta` — whether this installation has projects, the default currency, the categories, the period lock, the receipt threshold and the caller's permissions. Nothing in this package re-derives a rule. |
| `src/api/entries.ts` | Expenses: the list, one read, create, the revision-guarded replace, delete and submit. |
| `src/api/attachments.ts` | Receipts: the multipart upload, the delete, and where the bytes are read from. |
| `src/api/projects.ts`, `src/api/rates.ts`, `src/api/stats.ts` | The projects an expense may be booked on, the dated rate table, and the caller's key figures. |
| `src/api-schema.d.ts` | Generated from `openapi/expenses.yaml` by `bun run gen:client` — do not edit. |
| `src/lib/status.ts` | The statuses and kinds: the values the API answers, their i18n keys and colours. |
| `src/lib/money.ts`, `src/lib/rates.ts` | The VAT helper's arithmetic and the mileage preview — both previews of what the server will compute, never a substitute for it. |
| `src/lib/receipts.ts` | The size, count and type a receipt may have, and which of them the browser can draw. |
| `src/lib/dates.ts`, `src/lib/format.ts` | Calendar dates in UTC, and the one way this package writes money, dates and distances. |
| `src/lib/search.ts` | `validateMyExpensesSearch` — the host route's `validateSearch` for My expenses. |
| `src/lib/errors.ts` | What a refusal says, and which expense each per-id sentence is about. |
| `src/components/` | `ExpenseStatusBadge`, `StatusStrip`, `VatField`, `ReceiptThumbnails`, `ReceiptViewer`, `ReceiptDropzone`, `RefusalList`. |
| `src/pages/` | `my-expenses.tsx`, and the `-`-prefixed expense form it composes. |
| `src/test/` | Test-only: the fetch stub, an in-memory fake of the endpoints the pages use, fixtures, the stand-in route tree and the vitest setup. |
| `src/i18n.ts` | The `expenses` catalog in `en` and `nb`. |
| `src/index.ts` | The package entry point the host imports. |

## Boundaries

A module package never imports another module package or the host app;
`eslint.config.js` enforces that with `no-restricted-imports`. What this module
needs from projects it reads through `GET /api/v1/expenses/projects`, which the
backend answers from the project directory — so there is no projects code here.

Permissions are the host's answer, not the package's. `MyExpensesPage` takes the
caller's `userId` as a prop because the host owns the session; what may be done
to one expense comes from that expense's own `capabilities`, and what may be
done in the module comes from `GET /meta`. Nothing here reads
`/api/v1/identity/access/me`.

**Receipts are never framed.** The platform sends `X-Frame-Options: DENY` and
`frame-ancestors 'none'`, the host page's CSP carries `object-src 'none'`, and
the receipt download carries its own `default-src 'none'; sandbox`. JPEG and PNG
are drawn with `<img>` (an image subresource is not a document); HEIC and PDF get
a link that opens in a tab of its own. There is no `<iframe>`, `<object>` or
`<embed>` in this package, and a test asserts it.

## Commands

Run from the repository root:

```sh
mise exec -- bun run --cwd apps/expenses/frontend test
mise exec -- bun run --cwd apps/expenses/frontend typecheck
mise exec -- bun run --cwd apps/expenses/frontend lint
mise exec -- bun run gen:client         # regenerates src/api-schema.d.ts
mise exec -- bun run translations:check # every string in both en and nb
```
