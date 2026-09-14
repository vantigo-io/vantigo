# Frontend de-tenanting — implementation plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development to implement this plan task-by-task.

**Goal:** Remove tenancy from the SPA so it works against the single-tenant Go backend.

**Architecture:** Delete the `/api/v1/t/{slug}` URL rewrite and its module singleton, collapse the `$tenantSlug` route subtree up one level, delete the tenant selector/guard/capabilities/system-tenants surfaces, and finally strip the contract's tenant leftovers and regenerate all five API schemas.

**Tech Stack:** React 19, TanStack Router (file-based, committed `routeTree.gen.ts`), Mantine, Vite 8, Vitest 4, Bun workspaces, `openapi-typescript`.

**Spec:** `docs/superpowers/specs/2026-09-14-frontend-detenanting-design.md`

## Global Constraints

- **Baseline to restore after every task:** `bun run frontend:test` → exit 0, 65 test files, 284 tests, across 7 packages. Measured on clean `main`.
- **Typecheck is `bun run frontend:build`** (`tsc -b && vite build`, host only).
- **`routeTree.gen.ts` is committed and has NO drift check anywhere.** The TanStack Vite plugin regenerates it during `vite`, but `tsc -b` runs *first* in `frontend:build`. After any route move: run `vite build` (or `vite dev`) to regenerate, then run `frontend:build` again so `tsc` sees the fresh tree. Commit the regenerated file.
- **Five `api-schema.d.ts` copies** (`tools/openapi/gen-client.ts:10-15`). CI fails on drift (`ci.yml:73`). Regenerate all five together with `bun run gen:client`, never by hand.
- **biome excludes `routeTree.gen.ts` and `api-schema.d.ts`** (`biome.json:31-32`) — lint cannot catch staleness in either.
- **i18n:** `tools/i18n/validator.ts` raises only `invalid-catalog` (missing locale); there is **no unused-key check**, so deleting catalogs is safe and leaving unused keys is safe. `tools/i18n/source-check.ts` flags `jsx-text`/`jsx-prop` — **any JSX you touch must not introduce a raw string literal**.
- **The pre-commit hook runs a full gate**: `toolchain:check`, `translations:check`, `i18n:test`, `biome check`, `dotnet format`, `gofmt`. Never `--no-verify`. Never force-push.
- **No test in the frontend suite hits a real Go server.** String-level assertions are all that exist, so a prefix change can pass every check while being wrong. Task 9 exists for exactly this.

---

### Task 1: Delete the tenant URL rewrite

**Files:**
- Modify: `packages/frontend-api-client/src/index.ts:101-129,139-142`
- Rewrite: `packages/frontend-api-client/src/index.test.ts`
- Modify: `apps/host/frontend/src/api/request.ts:1-8`, `apps/host/frontend/src/main.tsx:42-43`

Delete `activeTenantSlug`, `tenantRoutingEnabled`, `setActiveTenantSlug`, `setTenantRoutingEnabled` and `tenantAwareUrl` outright (spec D1 — a dormant flag is the highest silent-breakage risk). `requestUrl` becomes `options.transformUrl(url)` with no rewrite. Remove the re-exports in `request.ts` and both calls in `main.tsx`.

`index.test.ts`: its `/api/v1/t/default/customers` assertions encode the deleted behaviour — **rewrite, don't adjust**. Add a test asserting a business URL passes through unchanged.

**Teeth:** reintroduce a rewrite in `requestUrl`; a test must fail.

### Task 2: Delete the tenant API surfaces

**Files:**
- Delete: `apps/host/frontend/src/api/system-tenants.ts`, `apps/host/frontend/src/api/tenant-capabilities.ts`
- Modify: `apps/host/frontend/src/api/auth.ts:9-24,37-47`

Remove `switchTenant`, the `Tenant` interface, and `tenants`/`activeTenantId` from `Session`. All three endpoints are absent from both the contract and the Go server (spec §1.2) — delete, do not port.

### Task 3: Collapse the `$tenantSlug` route subtree

**Files:** move the 22 route files from `apps/host/frontend/src/routes/$tenantSlug/*` up one level; delete `routes/$tenantSlug.tsx`, `routes/-tenant-routing.ts`, `routes/tenant-routing.test.ts`; regenerate `routes/../routeTree.gen.ts`.

Use `git mv`. Delete `legacyTenantPath` and the `__root.tsx` legacy redirects with it — they rewrite un-prefixed URLs *into* prefixed ones, which is now backwards. Watch the name collision: `/settings` (personal account) already exists and is distinct from `$tenantSlug/settings` (workspace admin) — resolve deliberately and say how.

Follow the routeTree ordering rule in Global Constraints.

### Task 4: Delete the tenant components and rewire the root

**Files:**
- Delete: `components/tenant-selector.tsx`, `components/tenant-module-guard.tsx` (+ its test)
- Modify: `routes/__root.tsx:87,123-163,244-281`, `routes/index.tsx:6-13`, `components/settings-layout.tsx:22-23`, `components/app-spotlight.tsx` (+ test), `navigation.ts` (+ test)

`tenant-module-guard.tsx` has two halves: tenant-module-enablement (goes) and permissions (keep, wherever it still applies — say where you put it). In `navigation.ts` remove `tenantScoped`, `tenantPath()`, `navSectionsForTenant()` and the slug threading in `visibleNavSections()`. `routes/index.tsx` redirects `/` to `/{slug}` — it needs a new destination.

### Task 5: Remove the shell's tenant switcher

**Files:** `packages/frontend-shell/src/app-shell-layout.tsx:30-34,45-48,123,136-258`, `packages/frontend-shell/src/index.ts`

Delete `ShellTenant`, the `tenants`/`activeTenantId`/`onTenantSwitch` props and the switcher `<Menu>`. Leaving the tenant keys in `src/i18n/catalogs/shell.ts` is safe (no unused-key check) — remove them anyway for tidiness, but never at the cost of an `invalid-catalog` (every locale must keep the same key set).

### Task 6: Trim the admin subtree

**Files:**
- Delete: `routes/admin/tenants/$tenantId.tsx`, `routes/admin/tenants/new.tsx`
- **Rename, do NOT delete:** `catalogs/tenant.ts` → `catalogs/error.ts`. The file is misnamed. Only nine of its keys were tenant-selector strings; the rest are app-wide error/loading keys consumed by eight components under `components/errors/` and by `routes/dashboard.tsx`, with `error-pages.test.tsx` asserting their rendered English. Deleting it drops those keys, `t()` falls back to raw key names, and because i18next's `TFunction` is loosely typed here **`tsc` will not catch it** — it surfaces only as failing error-page tests and broken UI copy.
- Modify: `routes/admin/index.tsx`, `routes/admin/index.test.tsx:8`, `catalogs/system-admin.ts`

**`routes/admin/` survives.** `-maintenance-controls.tsx` has zero tenant references and `index.test.tsx` tests maintenance, not tenants (spec D3). Trim `index.tsx` to its `<MaintenanceControls />` half; drop only the `vi.mock("../../api/system-tenants")` from the test; trim `system-admin.ts` to its non-tenant keys.

### Task 7: Remove slug threading from the module frontends

**Files (10):** `apps/customers/frontend/src/pages/{contacts.$contactId,contacts.index,-customer-contacts-card,customers.$customerId,customers.index}.tsx`; `apps/energy/frontend/src/pages/{-customer-meters-table,metering-points.$meteringPointId,metering-points.index}.tsx`; `apps/products/frontend/src/pages/{products.$productId,products.index}.tsx`

Two divergent idioms exist and **both** go (spec D5): `useParams({ strict: false })` and the raw `window.location.pathname.split("/")[1]` in `-customer-meters-table.tsx:49`, `products.index.tsx:40` and `products.$productId.tsx:61`. Links are built from the bare path.

### Task 8: Strip the contract's tenant leftovers (spec D4)

**Files:** `openapi/identity.yaml:225-265,1751`; `apps/server/internal/identity/sessions.go:24-26,46-47`; all five `api-schema.d.ts` via `bun run gen:client`; Go generated code via `go generate`.

Remove `activeTenantId`, `tenants`, `TenantSessionResponse`. Then, in order: `cd apps/server && go generate ./... && go test ./internal/openapi/... && cd ../.. && bun run gen:client` (CONTRIBUTING.md:336-343). Run the **Go** suite too — this is the only task that touches Go.

Sequenced last so it can be dropped without unpicking Tasks 1-7 if it proves messy.

### Task 9: The survival sweep

**Files:** a new test in `apps/host/frontend`, plus whatever it finds.

Add a test asserting **no `/api/v1/t/` and no `tenantSlug` string survives** in shipped frontend sources (exclude `routeTree.gen.ts`, `api-schema.d.ts`, and this test itself). This is the one check that catches a dormant prefix — the failure mode Global Constraints names, and the reason a green suite is not sufficient evidence here.

Then run the whole gate: `bun run frontend:test`, `bun run frontend:lint`, `bun run frontend:build`, `bun run gen:client && git diff --exit-code -- '**/api-schema.d.ts'`, `bun run translations:check`, `bun run i18n:test`, `bun run format:check`. Report each with its working directory.
