# Frontend de-tenanting — design

Sub-project 6 of the Go backend port (`2026-09-10-go-backend-port-design.md` §10.6, §4 item 4).

## 1. Scope

Remove tenancy from the frontend so the SPA works against the single-tenant Go
backend. The parent spec scopes this as: move `apps/host/frontend/src/routes/
$tenantSlug/*` up one level, delete `routes/admin/tenants/*`,
`components/tenant-selector.tsx` and the tenant capabilities calls, and remove
`setActiveTenantSlug`/`tenantAwareUrl` from `packages/frontend-api-client`.

Explicitly **not** in scope, both assigned to sub-project 7 (cutover) by §4
items 1 and 3: the Vite dev proxy target, and removing the
`ensureCsrfToken`/`X-XSRF-TOKEN` no-op.

## 1.1 The parent spec is wrong about this being independent

§4 item 4 says de-tenanting "is independent of the backend port (single-tenant
mode already sends no tenant prefix)". **It does send one.** Verified:

- `packages/frontend-api-client/src/index.ts:119-129` — `tenantAwareUrl`
  rewrites every `/api/…` URL except `/api/v1/identity/` into
  `/api/v1/t/{slug}/…`.
- `apps/host/frontend/src/main.tsx:43` — `setTenantRoutingEnabled(true)` runs
  **unconditionally** at bootstrap. Line 42 leaves the slug `undefined`, so the
  rewrite is inert only until a tenant route sets it, which
  `routes/$tenantSlug.tsx:17-29` and `routes/__root.tsx:149` do on arrival.
- `apps/server/internal/module/compose.go:106,128` — Go mounts modules flat at
  `/api/v1/<name>/`, and `:104` forwards "the request path unchanged (no
  StripPrefix)". No tenant segment is registered or stripped anywhere in Go.
- The prefix originates in .NET's `packages/tenancy/Vantigo.Tenancy/
  TenantRouteTemplates.cs`, still present.

**Consequence:** against the Go backend, every business-module call from a
tenant-scoped page 404s. This sub-project is a *prerequisite* for the port
serving a working app, not a parallel cleanup track.

## 1.2 Three frontend API surfaces call endpoints Go does not serve

Verified against `openapi/identity.yaml` and `apps/server/internal/identity/`:

| Frontend caller | Endpoint | In contract | In Go |
|---|---|---|---|
| `api/auth.ts` `switchTenant` | `POST /api/v1/identity/session/tenant` | no | no |
| `api/tenant-capabilities.ts` | `GET /api/v1/identity/tenants/current/capabilities` | no | no |
| `api/system-tenants.ts` (6 functions) | `/api/v1/identity/admin/tenants*` | no | no |

These are dead on arrival at cutover. They are deleted, not ported.

## 2. Decisions

**D1 — the `/api/v1/t/` prefix is deleted, not disabled.** Removing
`tenantAwareUrl`, `setActiveTenantSlug`, `setTenantRoutingEnabled` and the
module singleton outright, rather than leaving a flag defaulted to `false`. A
dormant rewrite with no compile-time link to routing or schema is the single
highest silent-breakage risk in this change (§4): it would compile, render, and
pass the suite while mis-routing. Cost if wrong: reinstating a prefix later
means a new function, which is the correct cost for a capability this port does
not have.

**D2 — the 22 `$tenantSlug` route files move up one level**, and
`routes/-tenant-routing.ts`, `components/tenant-selector.tsx` and
`components/tenant-module-guard.tsx` go. The guard's *permission* half is kept
wherever it still applies; only its tenant-module-enablement half is tenancy.
`legacyTenantPath` and the `__root.tsx` legacy redirects go with them — they
rewrite un-prefixed URLs *into* prefixed ones, which is now backwards.

**D3 — `routes/admin/tenants/*` and `api/system-tenants.ts` are deleted**
(5 admin route files). The control plane manages tenants; there are none.

**D4 — the contract's tenant leftovers are removed and all five schemas
regenerated.** `openapi/identity.yaml:225-265,1751` still declares
`activeTenantId`, `tenants` and `TenantSessionResponse`;
`apps/server/internal/identity/sessions.go` returns `[]` and `nil` for them and
its own comment calls this "the contract's leftover". De-tenanting that leaves
tenant fields in the contract is half-done, and leaving them means the frontend
compiles against phantom tenant data indefinitely. This expands the change into
Go (handler + `go generate`) and all five `api-schema.d.ts` copies. It is
sequenced **last**, as its own task, so it can be dropped without unpicking the
rest if it proves messy. Cost if wrong: sub-project 7 does it instead.

**D5 — the module frontends' two divergent slug-reading idioms both go.**
`apps/customers|products|energy|communications/frontend` read the slug either
via `useParams({ strict: false })` or via
`window.location.pathname.split("/")[1]`. Both are removed rather than
normalised; with no slug segment, links are built from the bare path.

## 3. Risk and verification

The recon's assessment is adopted: the highest silent-breakage risk is the API
prefix (D1), because it is a pure string rewrite with no compile-time link to
anything else, and **no test in the frontend suite hits a real Go server** —
`packages/frontend-api-client/src/index.test.ts` asserts at string level only.
A change here can pass `bun run frontend:test` and `bun run gen:client:test`
while every business-module call is broken.

Mitigation, in order:

1. **Baseline pinned before any edit:** `bun run frontend:test` on clean `main`
   is green — 65 test files, 284 tests, exit 0 across 7 packages. Every task
   restores this.
2. **The five `api-schema.d.ts` copies are regenerated together.**
   `tools/openapi/gen-client.ts:10-15` declares all five targets; a partial
   update has broken CI on this repo before. This failure is loud, not silent.
3. **`packages/frontend-api-client/src/index.test.ts` is rewritten, not
   adjusted.** Its assertions (`/api/v1/t/default/customers`) encode the
   behaviour being deleted; they must assert the *absence* of any rewrite.
4. **A test asserting no `/api/v1/t/` string survives anywhere** in the shipped
   frontend sources — the one check that catches a dormant prefix.

## 4. Out of scope

Mantine/TanStack versions, i18n tooling, the module frontends' own features,
the dev proxy, the CSRF no-op, and the .NET `packages/tenancy` tree (deleted in
sub-project 7 with the rest of the .NET source).
