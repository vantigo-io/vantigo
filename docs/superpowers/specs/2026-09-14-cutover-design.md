# Cutover — design

Sub-project 7 of the Go backend port (`2026-09-10-go-backend-port-design.md` §10.7), and the last.

## 1. Scope

Delete the .NET tree and its tooling, rewrite the deployment manifests and the
documentation, switch the Vite dev proxy to the Go server, remove the CSRF
no-op, and fold the Go-only CI workflows into the main pipeline.

After this, `ghcr.io/vantigo-io/vantigo` is built from `apps/server` and nothing
in the repository builds with `dotnet`.

## 1.1 The parent spec's deletion list is incomplete

§10.7 names the .NET tree, `global.json`, `.bun-version` and `tools/toolchain`.
**Six more files exist and are equally dead**, verified present at `24a82a8`:

| File | Why it must go |
|---|---|
| `.config/dotnet-tools.json` | `ci.yml`'s `metadata` job runs `dotnet tool restore` against it |
| `GitVersion.yml` | `metadata` runs `gitversion/setup`+`execute`; its output tags every published image. The Go side already uses `svu` |
| `Directory.Packages.props` | central NuGet pinning |
| `Directory.Build.props` | `TargetFramework net10.0`, container settings, `GitVersion.MsBuild` |
| `Directory.Build.targets` | `SetContainerTagsFromGitVersion`, hooked to `dotnet publish` |
| `Directory.Solution.targets` | `InstallFrontendDependencies`, an MSBuild `Publish` hook |

None carry a `.cs`/`.csproj`/`.slnx` extension, which is exactly why an
extension-based count missed them.

**`CLAUDE.md` does not exist.** §10.7 lists it for rewriting; nothing to do.

## 2. Decisions

**D1 — the pre-commit hook changes in the same commit as the deletion.**
`.husky/pre-commit` is `set -e` (line 2) and runs `dotnet format "Vantigo.slnx"`
*before* `gofmt -l apps/server`. Deleting the solution file blocks every commit
**and** the Go formatting check never runs. Remove the `dotnet format` lines and
`mise.toml`'s `dotnet` pin together with the tree.

The hazard is the repair, not the break: a hook that runs less looks exactly
like a hook that passes. **Any change here ships with a teeth check** —
introduce a formatting violation, confirm the hook still rejects the commit.

**D2 — `compose.yaml` is rewritten, and its CI guard is strengthened.**
`ci.yml`'s `validate_compose` runs only `docker compose config -q`: YAML and
interpolation. It will stay green while the manifest keeps
`ConnectionStrings__vantigo` (an ASP.NET binding convention the Go server never
reads) and `healthcheck.test: ["CMD", "/app/Vantigo.Host", "healthcheck"]`,
naming a binary that will not exist. **The production manifest can be entirely
wrong with a green pipeline.**

So: rewrite the manifest to `DATABASE_URL` and `/app/vantigo healthcheck`, and
add a cheap assertion to that job that the file contains no `Vantigo.Host` and
no `ConnectionStrings__` — a grep is not a deployment test, but it converts the
one silent failure in this sub-project into a loud one.

**Probes use the exec form**, per §10.7 — and this is now verified rather than
inherited: `internal/security/hostfilter.go:17` defines allowed hosts as
*APP_URL's host plus loopback*, wired at `internal/server/server.go:88`. An
`httpGet` probe sends the pod IP as `Host`, which is neither, so it is rejected.
The termination grace period must exceed `SHUTDOWN_TIMEOUT` (default 30 s, so
≥ 35 s).

**D3 — the Vite dev proxy switch is a real fix.** `vite.config.ts:8` falls back
to `http://localhost:10010`, the Aspire launch-profile port. Once AppHost is
deleted the injected variable is never set, and `mise.toml`'s `server:dev` task
serves **`:8080`** — so the fallback points at a closed port and local dev
proxies into nothing, silently. Point it at the Go dev port.

**D4 — documentation verdicts.** Three docs describe subsystems the Go port
does not have, confirmed by reading the code rather than inferring from names:

- **DELETE `docs/tenancy.md`** — no `tenant_id` column survives in any
  migration, and `internal/storage/storage.go`'s own package doc states the
  tenant segment is dropped. Nothing survives as active documentation.
- **DELETE `docs/data-protection-key-wrapping.md` and `docs/azure-identity.md`**
  — `internal/secrets` is an AES-256-GCM box keyed from `APP_SECRET` via
  HKDF-SHA256 with one derived key per purpose. There is no persisted key ring,
  no Key Vault, and no Azure integration anywhere in `apps/server`.
- **REWRITE the other ten**, plus `README.md`, `CONTRIBUTING.md` and
  `deploy/compose/README.md`.

**`docs/storage.md`'s S3/MinIO and Azure Blob sections are a capability
regression**: `internal/storage` contains only `fs.go`, and `objectStorage()`
accepts only `""` or `"fs"`. State that plainly rather than softening it.

**D5 — three docs assert something false about the Go server.**
`customers-authentication.md`, `sso-scim-operations.md` and
`deploy/compose/README.md` all say the API does not apply migrations at
startup. **`api` mode migrates before serving** (`cmd/vantigo/main.go`,
`case modeAPI:` calls `migrate` first). Correct it everywhere; do not carry it
forward.

**D6 — the recorded-exchange corpus is frozen, and the docs must say so.**
`openapi/testdata/exchanges/*.jsonl` is the only record of what .NET actually
served, consumed by `internal/openapi/exchanges_test.go`. Once the .NET tree is
gone it **can never be re-recorded** — yet `CONTRIBUTING.md:345-352` documents
exactly how to refresh it. Keep the corpus; rewrite those instructions to say it
is unregenerable historical evidence. Deleting it discards the ground truth the
contract was validated against; implying it can be refreshed is worse.

**D7 — provenance comments stay.** Roughly fifteen Go files cite `.cs` paths
they were ported from (`internal/storage/fs.go:318`, `internal/identity/access.go:77`,
`internal/mail/guard.go:26`, and more). These become dangling references. They
are kept: the git history retains the .NET tree, and the citations are the only
trace of *why* a behaviour is shaped as it is. `CONTRIBUTING.md` gains one line
saying such paths refer to the pre-cutover tree.

**D8 — the enabled-modules endpoint is out of scope.** The SPA now shows every
module unconditionally, because the capabilities endpoint is gone and no source
of truth replaced it. Exposing the enabled set is **new Go behaviour**, not a
migration step, and this is already the riskiest sub-project. Recorded as a
follow-up.

**D9 — the CSRF no-op removal is an API change, not a line.** `ensureCsrfToken`
is part of `frontend-api-client`'s public interface (`src/index.ts:23,120,150,187`)
and appears across 21 files. Removing it changes every consumer.

**D10 — the Go-only workflows fold in.** `server-ci.yml`, `server-release.yml`
and `server-test.yml` were written to merge into the main pipeline at cutover.
`ci.yml` loses `build_and_test_solution` wholesale, `dotnet tool restore`, both
`dotnet publish` steps, and the `csharp` CodeQL matrix entry — the last of which
is dormant behind `ENABLE_CODE_SCANNING`, so it would fail later rather than now.

## 3. Risk

The defining risk is that **documentation has no test**. Code that lies fails;
a doc that lies is followed. The largest single surface here is `CONTRIBUTING.md`
and `README.md`, which walk a contributor through `dotnet tool restore` and
`dotnet run --project orchestration/AppHost` as the standard bootstrap.

Mitigation is to write the rewrites from the code rather than by translating
.NET prose: `cmd/vantigo/main.go`'s header comment documents all six commands
(`api`, `server`, `worker`, `migrate`, `seed`, `healthcheck`), and
`internal/config/config.go`'s field comments are the authoritative environment
reference. `CONTRIBUTING.md:255-511` is already accurate for Go and is the
copy-from source, not a thing to re-derive.

## 4. Out of scope

New Go behaviour of any kind, the enabled-modules endpoint (D8), and any change
to the frontend beyond the dev proxy and the CSRF no-op.
