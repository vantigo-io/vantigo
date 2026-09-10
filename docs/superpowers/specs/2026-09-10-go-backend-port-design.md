# Vantigo backend port to Go — umbrella design

Date: 2026-09-10
Status: draft for review

This is the umbrella specification for replacing the .NET backend with a Go
application built, packaged and released the way Pjokk
(github.com/refsdal/pjokk) is. It fixes every cross-cutting decision once so
that the sub-projects listed in §10 can each get their own spec and plan
without re-litigating them.

## 1. Goals and non-goals

Goals

- One static Go binary serves the API and the existing React SPA from one
  process, in one image, with the same operational shape as today
  (`api`, `worker`, `migrate`, `seed`, `healthcheck` commands).
- Single-tenant only. One installation is one customer. Tenancy is a
  deployment concern, never a column.
- Spec-first API: the OpenAPI document is hand-maintained and is the single
  source of truth for the Go server, request validation and the frontend's
  types.
- Same product surface as today's .NET backend, with one deliberate reduction
  in Communications (§9).
- Build and release pipeline copied from Pjokk: mise-pinned toolchain,
  natively built multi-arch binaries, COPY-only distroless image, GoReleaser,
  svu-computed versions, cosign, SBOMs, PR preview images.
- As solid as the final product: fail-closed configuration, real-Postgres
  tests, the same security posture the .NET host has (CSP with script hash,
  host filtering, forwarded-header trust, transport rules, rate limiting).

Non-goals

- No data migration. Every installation starts from the new schema.
- No multi-tenant mode, no tenant control plane, no row-level security.
- No Azure Blob, Azure Key Vault or Azure identity integration. Object
  storage is a local volume behind a port (§3.8); more drivers are future
  work.
- No Mailgun (inbound or outbound), no inbound email at all, no attachment
  virus scanning (§9).
- No changes to the frontend beyond those listed in §4.

## 2. Decisions already taken

| Topic | Decision |
|---|---|
| Tenancy | Single-tenant. All tenant plumbing removed (backend and, as a separate track, the frontend's `$tenantSlug` routing). |
| Data | Fresh schema; no migration from the EF Core schema. |
| Identity scope | Everything from day one: local accounts, invitations, RBAC with permission catalog, TOTP MFA, recovery codes, passkeys, one static workforce OIDC provider (incl. Entra workload-identity client assertion), SCIM 2.0 server. |
| Storage | Local filesystem driver behind a `storage.Storage` port. |
| CSRF | Pjokk's model: `SameSite=Strict` cookie plus `Origin` check on unsafe methods. No token endpoint. |
| API contract | Spec-first for the backend from the first endpoint. The frontend gets generated types and a drift check from the start; call-site migration to a typed client is a follow-up per module. |
| Communications | Outbound SMTP only. Conversations, attachments, suppressions, retention and the AI draft feature stay. |
| Auth library | Hand-written on standard libraries (§3.7), not Limen. |

## 3. Target architecture

### 3.1 Repository layout

```
apps/server/                         Go module: github.com/vantigo-io/vantigo/server
  go.mod  go.sum  generate.go  .golangci.yml  sqlc.yaml
  cmd/vantigo/main.go                composition root + dispatch table
  internal/
    buildinfo/                       Version, stamped by ldflags
    config/                          env → validated Config, all errors at once
    db/                              pgxpool, goose runner, advisory lock
      migrations/                    NNNNN_<module>_<name>.sql (go:embed)
    httpx/                           response helpers, ProblemDetails, middleware
    web/                             go:embed'd SPA, index templating, fallback
    storage/                         Storage port, fs + memory drivers
    mail/                            Sender port, SMTP driver, SSRF guard, fake
    secrets/                         AES-GCM under the app secret
    ratelimit/                       fixed-window per-IP limiter
    contracts/                       cross-module interfaces + permission types
    identity/                        module (§3.7)
    customers/                       module
    products/                        module
    energy/                          module
    communications/                  module
    openapi/                         embedded copies of the spec files (generated)
openapi/                             hand-written spec: one file per module + common.yaml
scripts/                             build-artifacts.sh, spa-embed-overlay.sh, restore-embed-overlay.sh, build-image.sh
docker/data-skel/                    /data mountpoint skeleton owned by nonroot
Dockerfile                           COPY-only, distroless static nonroot
.goreleaser.yaml  .mise.toml  docker-compose.dev.yml  docker-compose.test.yml
```

Each module package has the same internal shape:

```
internal/<module>/
  module.go            Module value: name, permissions, Handler(deps), Workers(deps)
  gen/                 oapi-codegen output: types + strict server interface
  queries/*.sql        sqlc input
  dbgen/               sqlc output
  <feature>.go         handlers, one file per resource, mirroring the .NET endpoint files
  <feature>_test.go
```

The `apps/*/backend` and `packages/*` C# trees, `orchestration/`,
`Vantigo.slnx`, `Directory.*.props/targets`, `global.json`, `GitVersion.yml`
and `.config/dotnet-tools.json` are deleted in the cutover sub-project. The
bun workspace and all frontend packages stay where they are.

### 3.2 Process model

`cmd/vantigo` is the only place that reads the environment, opens the pool,
builds every collaborator and assembles a `Deps` struct per module. Nothing
below it calls `os.Getenv` or constructs a client.

Dispatch by `argv[1]`, mirroring `VantigoCommandLine` and Pjokk:

| Command | Behaviour |
|---|---|
| `api` | Migrate under the advisory lock, then serve API + SPA. Workers run in-process when `WORKERS_IN_PROCESS=1` (default `1` for single-container deployments). |
| `server` | HTTP only. Never migrates, never runs workers. What replicas run. |
| `worker` | Workers only, plus `/health/live` and `/health/ready`. |
| `migrate` | Apply migrations, exit 0/1. |
| `seed` | Development data. Refuses unless `APP_ENV=development`. |
| `healthcheck` | Probe `/health/ready` on this process's port; exit 0/1. Constructs nothing else. |
| no argument / unknown | Usage on stderr, exit 2. |

Migrations run under `pg_advisory_lock(0x56414E5449474F31)` exactly as
today, so `api` containers booting concurrently serialise. Orchestrated
deployments still run the explicit `migrate` one-off before rolling `server`
replicas.

Shutdown: SIGTERM drains HTTP for up to `SHUTDOWN_TIMEOUT` (default 30 s,
which must exceed the 20 s SMTP send cap so an in-flight outbox send commits
its completion). `ReadHeaderTimeout` and `IdleTimeout` are set; no whole-body
timeouts, because attachment uploads stream.

### 3.3 Configuration

Environment variables only, parsed in `internal/config` into one `Config`
struct. Validation reports every problem before exiting. Names follow
Pjokk's flat style; the .NET `Section__Key` names are retired and the
compose files are rewritten in the cutover.

Core:

| Variable | Meaning |
|---|---|
| `DATABASE_URL` | Runtime connection string. `sslmode=verify-full` required unless `ALLOW_INSECURE_TRANSPORT=1`. |
| `MIGRATIONS_DATABASE_URL` | Optional owner-role connection for `migrate`; defaults to `DATABASE_URL`. |
| `APP_URL` | Public origin (`https://…`). Drives host filtering, cookie `Secure`, the WebAuthn RP ID, OIDC redirect URIs, the `Origin` check, and email links. Must be https unless `ALLOW_INSECURE_TRANSPORT=1`. |
| `APP_BASE_PATH` | Optional path prefix the SPA and API are served under. |
| `APP_SECRET` | ≥32 bytes. Root secret; per-purpose keys are derived with HKDF (§3.7, §3.9). |
| `APP_ENV` | `production` (default) or `development`. Gates `seed`, dev-only behaviour, and the HSTS header. |
| `PORT` | Listen port, default `8080`. |
| `TRUSTED_PROXY_HOPS` | How many `X-Forwarded-*` hops to trust; `0` (default) trusts none. |
| `ALLOW_INSECURE_TRANSPORT` | Single escape hatch for plain-http `APP_URL`, non-verify-full Postgres and non-TLS SMTP. Logged loudly at boot. |
| `MODULES` | Comma-separated enabled modules; default `customers,communications,products,energy`. Communications and Energy require Customers; violations fail startup naming both. |
| `WORKERS_IN_PROCESS` | `1`/`0`; see §3.2. |
| `SHUTDOWN_TIMEOUT` | Duration, default `30s`. |
| `STORAGE_DRIVER` / `STORAGE_FS_PATH` | `fs` (only driver today) and its root, default `/data`. |
| `BOOTSTRAP_SECRET` | One-time secret for first-owner setup, as today. |
| `SMTP_*` | Host, port, user, password, from, TLS mode (§3.9). |
| `OIDC_*` | Issuer, client id, client secret or `AZURE_FEDERATED_TOKEN_FILE`, scopes, display name. All-or-nothing. |
| `SCIM_TOKEN`, `SCIM_PREVIOUS_TOKEN`, `SCIM_PREVIOUS_TOKEN_EXPIRES_AT` | Static SCIM bearer with rotation overlap, as documented in `docs/sso-scim-operations.md`. |
| `OPENAI_API_KEY`, `OPENAI_MODEL` | Optional; the AI draft feature is off without them. |
| `OTEL_EXPORTER_OTLP_*` | Standard OTel variables; a signal is exported only if its endpoint is set. |
| `SESSION_*` | Absolute and idle lifetimes, with the tighter Owner/SystemAdmin bounds carried over from `VantigoAuthenticationOptions`. |

Branding (`APP_TITLE`, `APP_LOGO_URL`, `APP_SUPPORT_EMAIL`, …) is kept for
the SPA index templating (§3.12).

### 3.4 Database

- `pgx/v5` with `pgxpool`; sqlc generates typed Go from `queries/*.sql` per
  module; goose applies embedded SQL migrations.
- **One schema per module** is preserved: `identity`, `customers`,
  `products`, `energy`, `communications`. No cross-schema references in
  migrations or queries; a test scans migration SQL and sqlc queries for
  `<other_schema>.` and fails on a hit (replacing the NetArchTest
  `DatabaseBoundaryTests`).
- **One goose lineage, one version table, all schemas always migrated.** A
  disabled module's schema is inert; migrating it unconditionally avoids
  partially-migrated installations and keeps `migrate` trivially idempotent.
  Migration files are named `NNNNN_<module>_<name>.sql` so ownership is
  visible.
- The initial migration per module is a squashed baseline written by hand
  from the current EF model, not a translation of the 38 EF migrations.
  Postgres-specific pieces stay as they are today: `btree_gist` and the
  GiST exclusion constraint on supply periods, `jsonb` for variant option
  values and message metadata, identity columns, `ON CONFLICT … RETURNING`,
  `FOR UPDATE`, transaction- and session-scoped advisory locks.
- No `tenant_id` anywhere. No RLS. The runtime role is still least-privilege
  (DML only, owns nothing) because it costs nothing; it is no longer
  load-bearing.
- Real transactions via `pool.BeginTx` with the sqlc `Queries` bound to the
  transaction. Unique-violation detection by `pgconn.PgError.Code ==
  "23505"`, never by message text.

### 3.5 HTTP layer

- Standard library `net/http` with Go 1.22+ method-and-path patterns. No
  framework.
- **One OpenAPI file per module** under `openapi/` (`identity.yaml`,
  `customers.yaml`, `products.yaml`, `energy.yaml`, `communications.yaml`)
  plus `openapi/common.yaml` for shared components (ProblemDetails,
  pagination, validation error shape). Module files reference common via
  `$ref` and oapi-codegen `import-mapping`. Per-module files keep each
  module's contract reviewable, let a disabled module's spec not be served,
  and give each frontend package exactly its own types.
- `oapi-codegen` (strict server, std-http) generates `internal/<module>/gen`.
  `oapi-codegen/nethttp-middleware` validates every request against the
  spec at runtime. Generated code is committed; a drift test runs `go
  generate` and fails on a diff, so neither CI nor the image runs codegen.
- Paths are unchanged from today: `/api/v1/identity/…`,
  `/api/v1/customers/…`, etc. The `/api/v{version}` URL segment stays as a
  literal `v1`; API versioning machinery is not ported.
- Errors are RFC 7807 ProblemDetails with the exact JSON shape the
  `frontend-api-client` package already parses (`title`, `detail`,
  `status`, `errors` for validation, `error.code` for rate limiting and
  domain codes). Unhandled panics become a sanitised 500 problem; nothing
  leaks stack traces.
- Middleware order on the API mux (top to bottom):
  recovery → request logging/tracing → forwarded-headers (trusted hops) →
  host filtering → security headers → rate limiting (named policies by
  route group) → session load → origin check on unsafe methods → spec
  validation → module handler.
- The combined spec (`/api/openapi.json`) and a Scalar page (`/api/docs`)
  are served behind an authenticated session only — never anonymously, in
  any environment.

### 3.6 Modules and boundaries

Each module exports one value:

```go
type Module struct {
    Name        string
    Permissions []contracts.Permission        // key "module:verb", display, sensitive, delegable
    Handler     func(Deps) http.Handler        // mounted at /api/v1/<name>
    Workers     func(Deps) []Worker            // may be empty
    Contracts   func(Deps) contracts.Set       // implementations it provides
}
```

Rules, carried over from `docs/module-boundaries.md`:

1. `internal/<module>` imports only `internal/contracts` and the platform
   packages (`db`, `httpx`, `storage`, `mail`, `secrets`, `config` types).
   Never another module. Enforced by `depguard` in golangci-lint.
2. `internal/contracts` depends on nothing inside the module tree.
   `CustomerDirectory`, `ProductCatalog`, `ApplicationEmailSender`, and the
   permission catalog types live here.
3. Endpoint and row types are unexported.
4. One schema per module (§3.4).
5. Module enablement is decided once at startup from `MODULES`; a disabled
   module contributes no handler, no workers, no permissions, and its paths
   answer 404 from the `/api` catch-all. The composition validator rejects
   Communications or Energy without Customers.
6. **Permission catalog validation at startup**: every operation in a
   module's spec declares its access rule with an `x-vantigo-access`
   extension: `anonymous`, `session` (any active account), a policy name
   from §3.7, or `permission:<module:verb>`. An operation without the
   extension fails the drift test; a permission key missing from the
   composed catalog fails startup. This replaces `ValidatePermissionCatalog`.

### 3.7 Identity

Hand-written on well-known libraries; no auth framework.

Sessions

- Opaque 256-bit random token in cookie `vantigo.session` (`HttpOnly`,
  `Secure` outside insecure-transport mode, `SameSite=Strict`,
  `Path=<base path or />`). Only the SHA-256 of the token is stored.
- `identity.sessions` row: user id, token hash, created, last seen, absolute
  expiry, idle expiry, `mfa_verified_at`, IP, user agent, `revoked_at`.
  Validation is one query per request. Idle expiry slides on use.
- Absolute and idle lifetimes, with the tighter Owner/SystemAdmin bounds,
  come from config (§3.3). Password change, MFA reset, disable, and
  `sessions/revoke` delete or revoke rows — no security stamps.
- This replaces ASP.NET Data Protection for cookies and tokens entirely.
  Data Protection's remaining use (mailbox credentials) is covered by
  `internal/secrets` (AES-256-GCM under an HKDF-derived key from
  `APP_SECRET`, with a key-id prefix so a future rotation can add keys).

Credentials

- Passwords: Argon2id (`golang.org/x/crypto/argon2`), PHC-formatted so
  parameters can change; parameters start at OWASP's current recommendation.
- Login attempt throttle per account and per IP, plus the nine named
  fixed-window rate-limit policies from
  `AuthRateLimitingServiceCollectionExtensions` (`Login`, `Bootstrap`,
  `Invitations`, `InvitationAcceptance`, `PasswordRecovery`, `Mfa`,
  `PasskeyLogin`, `UserManagement`, `OwnerAvatarRead`) with the same limits
  and the same `{"error":{"code":"rate_limited"}}` + `Retry-After` shape.
- Invitation, password-recovery and bootstrap tokens: 256-bit random, only
  the SHA-256 stored, as today.

MFA and passkeys

- TOTP via `pquerna/otp`; recovery codes are one-use, hashed at rest. Login
  becomes a two-step flow: `POST /login` answers `mfaRequired` with a
  short-lived login ticket; `POST /login/2fa` completes it. Owners must
  have MFA unless `OWNERS_ALLOW_INSECURE_NO_MFA=1` (carried over).
- Passkeys via `go-webauthn/webauthn`. RP ID and origin come from `APP_URL`,
  never the `Host` header. User verification required, resident keys
  required. Ceremony state lives in a short-TTL table, not in a cookie.

Workforce OIDC

- One static provider from `OIDC_*` via `coreos/go-oidc` and
  `golang.org/x/oauth2`, authorization code + PKCE, issuer re-validated on
  callback. Federated identity key = SHA-256(issuer + subject), as today.
- Entra workload identity: when `AZURE_FEDERATED_TOKEN_FILE` is set, the
  token endpoint is called with `client_assertion` read from that file
  instead of a client secret.
- Endpoints, redirect handling and the external-login cookie mirror the
  current `WorkforceOidcEndpoints`.

SCIM 2.0

- Hand-written server for the current 13 endpoints under
  `/api/v1/identity/scim/v2`: ServiceProviderConfig, Schemas,
  ResourceTypes, Users and Groups CRUD + list with filters, PATCH, ETags.
- Static bearer token(s) with the documented rotation overlap; ingress rate
  limited.

Authorization

- Permission catalog as in §3.6. Roles, role metadata, access groups with
  group→role mappings, delegations, and the authorization audit trail are
  ported as they are, including the per-role transaction advisory lock in
  the mutation path.
- Policies (`ActiveAccount`, `SystemAdmin`, `Owner`, `OwnerManagement`,
  `Business`, `AuthorizationManagement`, MFA-authenticated) become
  middleware helpers applied per route group.

Bootstrap: `/setup` with `BOOTSTRAP_SECRET`, the system-admin bootstrapper,
and `bootstrap-status` are ported unchanged. `TenantBootstrapper`,
`TenantDirectory`, `TenantMembershipService`, `TenantModuleCatalog`, the
`/admin/tenants` control plane and `tenants/current/capabilities` are not.

### 3.8 Object storage

```go
type Storage interface {
    Put(ctx, key string, r io.Reader, size int64, contentType string) error
    Get(ctx, key string) (io.ReadCloser, ObjectInfo, error)
    Exists(ctx, key string) (bool, error)
    Delete(ctx, key string) error
}
```

- Keys are `<scope>/<relative-key>`; `storage.Key` validates scope
  (`[a-z0-9-]`, ≤64) and rejects traversal, encoded separators, backslashes,
  control characters and keys over 1024 bytes — the current `StorageKey`
  rules.
- `fs` driver: root from `STORAGE_FS_PATH`, path-containment check after
  `EvalSymlinks`, atomic write via temp file + rename, permission `0600`.
  `memory` driver for tests.
- Downloads always stream through an authorised endpoint; nothing is
  served from storage directly. Adding `s3` later is a new file that
  satisfies the interface and a new `STORAGE_DRIVER` value.
- `/health/ready` includes a storage probe (writable root).

### 3.9 Email

- `mail.Sender` port; `smtp` driver on `wneessen/go-mail` with implicit or
  STARTTLS as configured; plain SMTP refused unless
  `ALLOW_INSECURE_TRANSPORT=1`.
- The SSRF/DNS-rebinding guard from `SmtpDestinationGuard` is ported:
  configured hosts are resolved and rejected if private, link-local,
  loopback or metadata addresses, re-checked immediately before connect.
- Identity's transactional mail (invitations, password recovery, MFA reset)
  and Communications' channel delivery both use the port. Communications'
  per-channel SMTP credentials are stored encrypted with `internal/secrets`.
- A `fake` sender records messages in memory for tests.

### 3.10 Background work

- A `Worker` is `Run(ctx) error` with a poll interval; the worker runner
  starts all enabled modules' workers as goroutines in `api` (when
  `WORKERS_IN_PROCESS=1`) and `worker` modes, never in `server` mode.
- Communications keeps three workers: outbox delivery, retention, and
  attachment cleanup. The outbox semantics in `docs/communications.md`
  are preserved exactly: claim by conditional update with lease, commit a
  `delivery_attempted_at` marker before the external send, complete after,
  deterministic `Message-Id`, exponential backoff capped at 3600 s,
  `max_attempts` terminal, possible-duplicate counter.
- The advisory-lock lease (`CommunicationsAdvisoryLease`) is ported so two
  workers never process the same queue.

### 3.11 Security, transport and CSRF

- Security headers on every response, API responses included (as the .NET
  host does today), byte-for-byte the current set: CSP built from the SPA inline-script hash (§3.12) with the same
  directives as `SecurityHeaders.cs`, `X-Content-Type-Options`,
  `X-Frame-Options: DENY`, `Referrer-Policy: no-referrer`, the same
  `Permissions-Policy`. CSP report-only mode stays as a flag. HSTS outside
  development, after forwarded headers are applied.
- Host filtering: only the `APP_URL` host and loopback are accepted.
- Forwarded headers honoured only for `TRUSTED_PROXY_HOPS` hops.
- Transport fail-closed rules from `docs/transport-security.md` are
  enforced at config validation with the single `ALLOW_INSECURE_TRANSPORT`
  escape hatch.
- CSRF: unsafe methods require an `Origin` header (or
  `Sec-Fetch-Site: same-origin`) matching `APP_URL`; otherwise 403. Combined
  with `SameSite=Strict` this replaces the antiforgery cookie/header pair.
  The `GET /api/v1/identity/antiforgery` endpoint is removed from the spec.

### 3.12 SPA serving

Ported from `SpaIndexDocument` and Pjokk's `internal/web`:

- The Vite build is overlaid into `internal/web/dist` by
  `scripts/spa-embed-overlay.sh` and `go:embed`'d. A committed placeholder
  `index.html` keeps `go build` and `go test` working without a frontend
  build.
- At startup the embedded `index.html` is templated once: asset URLs are
  rewritten for `APP_BASE_PATH`, `<title>` is replaced, and
  `window.__VANTIGO_APP__` (base path, title, logo URL, support contacts) is
  injected as the single inline script. Its SHA-256 goes into `script-src`,
  derived from the same string, so the CSP cannot drift.
- Routing: `/api/*` and the two health paths go to the API mux; a real
  embedded file is served with immutable caching for hashed assets; every
  other GET serves the templated index with `Cache-Control: no-cache`.
  `/index.html` is rewritten to `/`.
- Health: `/health/live` (no dependencies) and `/health/ready` (Postgres
  `SELECT 1`, storage probe). Both anonymous, unlimited, unchanged paths.

### 3.13 Observability

- `log/slog` JSON to stdout.
- OpenTelemetry SDK with `otelhttp`, `otelpgx`, runtime metrics, and the
  `vantigo.communications.*` meter. Each of traces/metrics/logs gets an
  OTLP exporter only if its endpoint is configured; otherwise the SDK is a
  no-op. SPA/static/health paths are excluded from tracing.
- Version from `buildinfo.Version` in the boot log, `/health/ready` body
  and the OTel resource.

## 4. Frontend impact

Frontend packages, the Mantine/TanStack stack, i18n tooling and tests are
unchanged except for:

1. `packages/frontend-api-client`: remove `ensureCsrfToken`/`X-XSRF-TOKEN`
   (keep the function as a no-op during the transition), remove
   `setActiveTenantSlug`/`tenantAwareUrl`.
2. `bun run gen:client` generating `src/api-schema.d.ts` into each module's
   frontend package from its `openapi/<module>.yaml`, plus a CI drift check.
   Generated files are committed and excluded from biome.
3. Vite dev proxy target moves from the Aspire-injected port to the Go
   server's port.
4. **De-tenanting (separate track):** move `apps/host/frontend/src/routes/
   $tenantSlug/*` up one level, delete `routes/admin/tenants/*`,
   `components/tenant-selector.tsx` and the tenant capabilities calls. This
   is independent of the backend port (single-tenant mode already sends no
   tenant prefix) and can run in parallel with it, but must land before the
   cutover because the Go Identity module does not serve the tenant
   endpoints.

Everything else keeps working because the spec is derived from what the .NET
host serves today (§10, sub-project 2).

## 5. Build, packaging and release

Copied from Pjokk, with names changed:

- `.mise.toml` is the single toolchain pin: Go, Bun, sqlc, oapi-codegen,
  goose, goreleaser, golangci-lint, svu, cosign, syft, trivy. `global.json`,
  `.bun-version` and `tools/toolchain` are removed.
- `scripts/build-artifacts.sh`: builds the SPA, overlays it into the embed
  dir, cross-compiles `CGO_ENABLED=0 GOOS=linux` for amd64 and arm64 with
  `-trimpath -ldflags "-s -w -X …/buildinfo.Version=$VERSION"`, restores the
  overlay on exit.
- `Dockerfile`: `FROM gcr.io/distroless/static-debian12:nonroot@sha256:…`,
  `COPY docker/data-skel/ /data/` owned by nonroot, `COPY
  ${BINARY_ROOT}/${TARGETPLATFORM}/vantigo /app/vantigo`, `HEALTHCHECK CMD
  ["/app/vantigo", "healthcheck"]`, `ENTRYPOINT ["/app/vantigo"]`. Nothing
  compiles inside Docker. `.dockerignore` is an allowlist.
- `.goreleaser.yaml`: binaries, `tar.gz` archives, checksums, syft SBOMs,
  keyless cosign on the checksum file, `dockers_v2` multi-arch image to
  `ghcr.io/vantigo-io/vantigo` with the tag ladder (`X.Y.Z`, `X.Y`, `X`,
  `latest`, `sha-<commit>`), cosign on the image, Conventional-Commits
  changelog grouped by type, GitHub Release.
- Versioning: `svu next --v0` from Conventional Commits. GitVersion is
  removed.
- `internal/buildinfo.Version` is the one version string: image tag, boot
  log, and the SPA's persisted-cache buster.

## 6. CI

Three workflows, as in Pjokk:

- `test.yml` (reusable): mise toolchain, `bun install --frozen-lockfile`,
  `bun audit --audit-level=high`, `bun run check` (biome, eslint, i18n
  gates, typecheck), spec drift (`go generate` + `gen:client` produce no
  diff), `goreleaser check`, frontend tests, `golangci-lint`, `go test -p 1
  -count=1 ./...` against a Postgres service container, `govulncheck`.
- `ci.yml` (pull requests): `test.yml`, then `build-artifacts.sh`, a
  single-arch `docker build`, and a **smoke test of the image**: `migrate`
  runs from the image, the server answers `/health/ready` with `ok`, `/`
  serves the SPA title, an unauthenticated `/api/v1/identity/session` gives
  401 (proves the auth path links and queries), a bad login writes a
  throttle row, `worker` starts and answers its health probe. Then trivy
  (HIGH/CRITICAL, exit 1), and a multi-arch push of preview tags
  `<next>-pr.<n>` and `<next>-pr.<n>.<sha>` when the PR is not from a fork.
- `release.yml` (push to main): compute version with svu; if releasable,
  re-run `test.yml` on the merge commit, tag, `goreleaser release`, delete
  the tag on failure. `workflow_dispatch` offers `dry_run` and
  `allow_major`.
- CodeQL stays, switched to `go` and `javascript-typescript`.
- Kept from today: the non-root OCI `User` assertion, the image-index
  platform assertion, trivy, CycloneDX/SPDX SBOM artifacts, cosign, SHA-pinned
  actions.

## 7. Testing strategy

- Go tests run in-process against a **real Postgres**
  (`docker-compose.test.yml` locally, a service container in CI), `-p 1`
  because packages truncate shared tables. Each module test builds its
  `Deps` with the memory storage, the fake mail sender, and a real pool,
  then drives the module's `Handler` through `httptest`.
- The 742 existing xunit tests are the behavioural reference. Each
  sub-project ports the tests for its area first (TDD), keeping the
  original test names in a comment so coverage can be audited against the
  .NET suite.
- Architecture tests: `depguard` for import rules; a Go test scanning
  migrations and queries for cross-schema references; a test asserting every
  spec operation carries `x-vantigo-access` and that every permission key it
  names is in the catalog; the spec drift tests.
- Generated code (oapi-codegen, sqlc, `api-schema.d.ts`) is committed and
  drift-checked; it is never regenerated in CI or in the image.
- The CI image smoke test (§6) is the only test that exercises the linked
  binary; it is deliberately fixture-free.

## 8. Development workflow

- `docker-compose.dev.yml`: Postgres, Mailpit (SMTP sink with a web UI).
  Aspire, MinIO, ClamAV and pgAdmin are gone.
- `mise run dev`: `go run ./cmd/vantigo api` with `APP_ENV=development
  ALLOW_INSECURE_TRANSPORT=1` plus `bun run dev` in `apps/host/frontend`
  proxying `/api` to it. `mise run test`, `mise run check`, `mise run
  artifacts`, `mise run image`, `mise run snapshot` as in Pjokk.
- `seed` populates development data (replacing the Bogus seeders) with
  `gofakeit`.

## 9. Communications scope for this port

Kept: channels (SMTP only), conversations, messages, composer, attachments
(on `fs` storage, same size limits), suppressions, retention, the outbox and
its workers, the AI draft/customer-suggestion feature (plain HTTPS calls to
the OpenAI API, on only when configured), the `Vantigo.Communications`
metrics.

Removed: Mailgun outbound provider, Mailgun inbound webhook and its
signature verification, MIME building, inbound HTML sanitisation and the
inbound worker, the ClamAV client and the scanner worker. The channel
model keeps a `kind` column so a future inbound or non-SMTP provider is
additive. The Communications frontend keeps working: channel forms only
offer SMTP, and inbound-only UI states are simply never reached.

## 10. Sub-projects and order

Each gets its own spec and plan against this document.

1. **Skeleton and pipeline.** `apps/server` module, dispatch, config,
   pool/goose/advisory lock, `httpx`, `web` with the embedded SPA and index
   templating, security headers, host filtering, health, rate limiter,
   `buildinfo`; `.mise.toml`, scripts, Dockerfile, GoReleaser, the three
   workflows with the smoke test. Ends with a Go image that serves today's
   SPA, answers 404 on `/api`, and is released by the new pipeline as a
   preview image.
2. **Contract.** Run the .NET host in Development, dump the OpenAPI
   documents, split and curate them into `openapi/*.yaml` (drop tenant,
   antiforgery, Mailgun and scanning operations; add
   `x-vantigo-access`), set up oapi-codegen, the validation
   middleware, `gen:client`, and the drift tests. No handlers yet.
3. **Identity.** Sessions, passwords, bootstrap, users, invitations,
   password recovery, RBAC + catalog + audit, account settings, then MFA,
   passkeys, OIDC, SCIM. Identity's transactional mail via the `mail` port.
4. **Customers, Products, Energy.** Independent; run in parallel. Includes
   Brreg lookup, the GiST constraint and the aggregation SQL.
5. **Communications.** Storage `fs` driver, secrets, outbox and workers,
   SMTP channel, attachments, suppressions, retention, AI drafts.
6. **Frontend de-tenanting.** Parallel track, any time after 2.
7. **Cutover.** Rewrite `deploy/compose`, `docs/*.md`, `README.md`,
   `CONTRIBUTING.md`, `CLAUDE.md`; delete the .NET tree and its tooling;
   switch the frontend dev proxy; remove the CSRF no-op.

Until step 7 the .NET backend remains the one that is deployed; the Go
server is built and smoke-tested by CI from step 1 but published only as
preview images.

## 11. Decisions taken in this spec that deserve a second look

- One goose lineage that migrates every module's schema even when the
  module is disabled (§3.4).
- Per-module OpenAPI files with a shared `common.yaml` rather than one
  document (§3.5).
- Access rules declared in the spec via `x-vantigo-access` rather than in
  Go (§3.6).
- `MODULES=` as a comma list instead of one flag per module (§3.3).
- Health paths stay `/health/live` and `/health/ready` rather than Pjokk's
  `/healthz` and `/readyz` (§3.12).
- The `/api/openapi.json` + Scalar page served behind a session in every
  environment, instead of never in production (§3.5).
