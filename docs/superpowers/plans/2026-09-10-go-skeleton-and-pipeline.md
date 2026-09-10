# Go Skeleton and Pipeline Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** A single static Go binary (`apps/server`) that serves today's React SPA and a production-grade HTTP skeleton (health, security headers, host filtering, CSRF, problem responses, migrations, rate limiter, telemetry), packaged in a COPY-only distroless image and built, smoke-tested and released the way Pjokk is — running alongside the .NET pipeline until the cutover.

**Architecture:** `cmd/vantigo` is the only composition root and dispatch table (`api | server | worker | migrate | seed | healthcheck`). Small platform packages under `apps/server/internal/` each own one concern and are tested in isolation; `internal/server` assembles them into the one `http.Handler`, and its tests drive the full middleware stack. PostgreSQL tests run against a real server, each test in its own throwaway database (`internal/testdb`).

**Tech Stack:** Go 1.27 (stdlib `net/http`, `log/slog`, `embed`), pgx v5 + pgxpool, goose v3 (library), OpenTelemetry Go SDK (OTLP http/protobuf), golangci-lint v2, GoReleaser v2, svu, cosign, syft, trivy, distroless `static-debian12:nonroot`, GitHub Actions, mise.

**Spec:** `docs/superpowers/specs/2026-09-10-go-backend-port-design.md` — this plan implements sub-project 1 (§10, "Skeleton and pipeline"), plus the observability section (§3.13), which the spec does not assign to a later sub-project.

## Global Constraints

- Go module path: `github.com/vantigo-io/vantigo/server`; module root `apps/server`; `go 1.27.0` in `go.mod`, matching `go = "1.27.0"` in `mise.toml` (enforced by `bun run toolchain:check`, Task 1).
- Tool pins (all in `mise.toml`): go 1.27.0, golangci-lint 2.13.2, goreleaser 2.18.0, svu 3.4.1, cosign 3.1.3, syft 1.51.1, trivy 0.74.0 (already pinned), actionlint 1.7.12, shellcheck 0.11.0. govulncheck v1.7.0 runs as `go run golang.org/x/vuln/cmd/govulncheck@v1.7.0` (a `go:` tool in mise.toml would need Go installed before mise could build it, which is fragile in CI).
- Commands assume a shell where mise is activated. Otherwise prefix them with `mise exec --`.
- Nothing below `cmd/vantigo` reads `os.Getenv` or constructs a collaborator, except `internal/telemetry`, where the OpenTelemetry SDK reads the standard `OTEL_*` variables itself.
- Every PostgreSQL-touching test uses `internal/testdb` against the server in `docker-compose.test.yml` (`127.0.0.1:55432`) or `TEST_DATABASE_URL`. No mocks of the database. Start it with `docker compose -f docker-compose.test.yml up -d --wait`.
- The race detector needs cgo and a C compiler; the dev machine has no `gcc`, so `-race` runs in CI only. Locally: `go test -count=1 ./...`.
- Health paths stay `/health/live` and `/health/ready`. Container port 8080. Image `ghcr.io/vantigo-io/vantigo`.
- Error responses are RFC 7807 problems (`application/problem+json`) with `type`, `title`, `status`, optional `detail`, and `traceId`. Nothing from an internal error ever reaches a response body.
- The .NET pipeline (`.github/workflows/ci.yml`, `apps/**/backend`, `packages/**`) is not modified. The Go pipeline lives in new `server-*.yml` workflows and publishes only PR preview tags (`<next>-pr.<n>`, `<next>-pr.<n>.<sha>`), never `latest` or a release tag.
- Commits follow Conventional Commits and end with the session's `Co-Authored-By` / `Claude-Session` trailers.

## Deviations from the spec, decided here

- **CSRF** uses Go's `net/http.CrossOriginProtection` (Go 1.25+) instead of a hand-written "require an `Origin` header" check. It rejects cross-site unsafe requests using `Sec-Fetch-Site`, falling back to `Origin` against `Host`, and lets through requests that carry neither header. Browsers always send one of them, so this still stops CSRF, and it doesn't break non-browser clients such as SCIM provisioning (sub-project 3), which a strict `Origin` requirement would.
- **`-p 1` is not needed.** The spec copied Pjokk's `go test -p 1`. That flag existed because Pjokk's packages truncate shared tables. Here every test gets its own database (`internal/testdb`), so packages run in parallel.
- **Transport rules are relaxed in development**, as the .NET host does today ("outside Development the application fails closed"). The spec only mentioned `ALLOW_INSECURE_TRANSPORT`. Both now switch the rules off.
- **Config variables not needed yet** (`APP_SECRET`, `MODULES`, `WORKERS_IN_PROCESS`, `STORAGE_*`, `SMTP_*`, `OIDC_*`, `SCIM_*`, `OPENAI_*`, `SESSION_*`, `BOOTSTRAP_SECRET`) are added by the sub-project that first uses them.

## File Structure

```
mise.toml                                   MODIFY  Go + release tool pins, server tasks
package.json                                MODIFY  toolchain:test script
tools/toolchain/check.ts                    MODIFY  go.mod ↔ mise go pin check
tools/toolchain/check.test.ts               CREATE
.gitignore                                  MODIFY  /dist/
.husky/pre-commit                           MODIFY  gofmt gate
.github/dependabot.yml                      MODIFY  gomod + docker ecosystems
.github/workflows/server-test.yml           CREATE  reusable Go quality gate
.github/workflows/server-ci.yml             CREATE  PR: gate → image → smoke → trivy → preview push
.github/workflows/server-release.yml        CREATE  manual GoReleaser dry run
.goreleaser.yaml                            CREATE
Dockerfile  .dockerignore                   CREATE  COPY-only distroless image
docker-compose.test.yml                     CREATE  PostgreSQL for tests
docker-compose.dev.yml                      CREATE  PostgreSQL for `mise run server:dev`
scripts/spa-embed-overlay.sh                CREATE  build SPA → internal/web/dist
scripts/restore-embed-overlay.sh            CREATE  put the placeholder back
scripts/build-artifacts.sh                  CREATE  native linux/amd64+arm64 binaries
scripts/build-image.sh                      CREATE  multi-arch buildx (verify or push)
scripts/smoke-image.sh                      CREATE  end-to-end check of a built image
CONTRIBUTING.md                             MODIFY  "Go server (port in progress)" section
apps/server/
  go.mod  go.sum  .golangci.yml
  cmd/vantigo/main.go  main_test.go         dispatch, composition, serve/drain
  internal/buildinfo/                       Version (ldflags)
  internal/config/                          env → validated Config
  internal/httpx/                           problems, request id/trace id, recover, request log,
                                            forwarded headers, base path, Chain
  internal/security/                        security headers + CSP, host filtering
  internal/web/                             embedded SPA, index templating, SPA handler
    dist/index.html                         committed placeholder
  internal/db/                              pool, retrying connect, goose runner + advisory lock
    migrations/00001_platform_init.sql
  internal/testdb/                          per-test throwaway databases
  internal/ratelimit/                       PostgreSQL fixed-window limiter + middleware
  internal/health/                          /health/live, /health/ready, Probe
  internal/server/                          assembles the production handler
  internal/telemetry/                       OTel providers, slog fan-out, otelhttp wrapper
```

---

### Task 1: Toolchain pins and the Go module skeleton

**Files:**
- Modify: `mise.toml`
- Modify: `tools/toolchain/check.ts`
- Create: `tools/toolchain/check.test.ts`
- Modify: `package.json` (scripts)
- Create: `apps/server/go.mod`, `apps/server/.golangci.yml`
- Create: `apps/server/internal/buildinfo/buildinfo.go`, `apps/server/internal/buildinfo/buildinfo_test.go`

**Interfaces:**
- Produces: `buildinfo.Version string` (default `"dev"`, stamped with `-ldflags -X github.com/vantigo-io/vantigo/server/internal/buildinfo.Version=<v>`); `checkToolchain(root)` now also reports `go` drift; `bun run toolchain:test`.

- [ ] **Step 1: Write the failing toolchain test**

Create `tools/toolchain/check.test.ts`:

```ts
import { afterEach, describe, expect, it } from "bun:test";
import { mkdir, mkdtemp, rm, writeFile } from "node:fs/promises";
import { tmpdir } from "node:os";
import { join } from "node:path";
import { checkToolchain } from "./check";

const roots: string[] = [];

const repository = async (files: Record<string, string>): Promise<string> => {
  const root = await mkdtemp(join(tmpdir(), "toolchain-"));
  roots.push(root);
  for (const [path, content] of Object.entries(files)) {
    await mkdir(join(root, path, ".."), { recursive: true });
    await writeFile(join(root, path), content);
  }
  return root;
};

const consistent = {
  "mise.toml": '[tools]\ndotnet = "10.0.302"\nbun = "1.3.14"\ngo = "1.27.0"\n"go:golang.org/x/vuln/cmd/govulncheck" = "1.7.0"\n',
  "global.json": JSON.stringify({ sdk: { version: "10.0.302" } }),
  ".bun-version": "1.3.14\n",
  "apps/server/go.mod": "module github.com/vantigo-io/vantigo/server\n\ngo 1.27.0\n",
};

afterEach(async () => {
  await Promise.all(roots.splice(0).map((root) => rm(root, { recursive: true, force: true })));
});

describe("toolchain check", () => {
  it("passes when every pin agrees", async () => {
    expect(await checkToolchain(await repository(consistent))).toEqual([]);
  });

  it("reports a go.mod go directive that differs from the mise go pin", async () => {
    const root = await repository({
      ...consistent,
      "apps/server/go.mod": "module github.com/vantigo-io/vantigo/server\n\ngo 1.28.0\n",
    });

    expect(await checkToolchain(root)).toEqual([
      { tool: "go", message: "mise.toml pins 1.27.0 but apps/server/go.mod declares go 1.28.0." },
    ]);
  });

  it("reports a missing go pin", async () => {
    const root = await repository({
      ...consistent,
      "mise.toml": '[tools]\ndotnet = "10.0.302"\nbun = "1.3.14"\n',
    });

    expect(await checkToolchain(root)).toEqual([{ tool: "go", message: "mise.toml does not pin a go version." }]);
  });

  it("does not mistake go-installed tools for the go pin", async () => {
    const root = await repository({
      ...consistent,
      "mise.toml": '[tools]\ndotnet = "10.0.302"\nbun = "1.3.14"\n"go:golang.org/x/vuln/cmd/govulncheck" = "1.7.0"\n',
    });

    expect(await checkToolchain(root)).toEqual([{ tool: "go", message: "mise.toml does not pin a go version." }]);
  });
});
```

- [ ] **Step 2: Run it to verify it fails**

Run: `bun test tools/toolchain`
Expected: FAIL. The first test passes, and the go-drift and missing-pin tests fail with `[]` because `checkToolchain` doesn't know about `go` yet.

- [ ] **Step 3: Extend the check**

In `tools/toolchain/check.ts`, add a go.mod reader below `readMiseVersion`:

```ts
const readGoDirective = (goMod: string): string | null => {
  const match = goMod.match(/^go\s+(\S+)\s*$/m);
  return match?.[1] ?? null;
};
```

Inside `checkToolchain`, after the existing `bun` check and before `return issues;`, add:

```ts
  const miseGo = readMiseVersion(miseToml, "go");
  const goMod = await readFile(resolve(repositoryRoot, "apps/server/go.mod"), "utf8");
  const goDirective = readGoDirective(goMod);

  if (miseGo === null) {
    issues.push({ tool: "go", message: "mise.toml does not pin a go version." });
  } else if (miseGo !== goDirective) {
    issues.push({
      tool: "go",
      message: `mise.toml pins ${miseGo} but apps/server/go.mod declares go ${goDirective}.`,
    });
  }
```

Change the success message in `runCheck` to:

```ts
  console.log("Toolchain checks passed (mise.toml agrees with global.json, .bun-version and apps/server/go.mod).");
```

`readMiseVersion`'s regex is anchored at `^\s*go\s*=`, so quoted keys such as `"go:golang.org/x/vuln/cmd/govulncheck"` never match. The last test pins that.

In `package.json` `scripts`, add after `"toolchain:check"`:

```json
    "toolchain:test": "bun test tools/toolchain",
```

- [ ] **Step 4: Pin the toolchain**

Replace `mise.toml` with:

```toml
# Single source of truth for the toolchain, used identically by developers and CI.
# Keep in sync with global.json (dotnet), .bun-version (bun) and the go
# directive in apps/server/go.mod — enforced by `bun run toolchain:check`,
# which runs in the pre-commit hook and in CI.
[tools]
dotnet = "10.0.302"
bun = "1.3.14"
go = "1.27.0"
# Used by CI to scan container images.
trivy = "0.74.0"

# Go server: lint, release and supply-chain tools. CI installs only the subset
# each job needs (mise-action install_args).
"aqua:golangci/golangci-lint" = "2.13.2"
"aqua:goreleaser/goreleaser" = "2.18.0"
"aqua:caarlos0/svu" = "3.4.1"
"aqua:sigstore/cosign" = "3.1.3"
"aqua:anchore/syft" = "1.51.1"
"aqua:rhysd/actionlint" = "1.7.12"
"aqua:koalaman/shellcheck" = "0.11.0"
```

- [ ] **Step 5: Create the Go module**

Write `apps/server/go.mod` by hand (`go mod init` would write an older language version):

```
module github.com/vantigo-io/vantigo/server

go 1.27.0
```

Create `apps/server/.golangci.yml`:

```yaml
# golangci-lint for the Go server. Run from apps/server:
#
#   golangci-lint run
#
# The standard linter set (errcheck, govet, ineffassign, staticcheck, unused)
# plus gofmt: a gate that is green the day it lands is one that stays on.
# Adding a linter is its own change with its own diff.
version: "2"

linters:
  default: standard

formatters:
  enable:
    - gofmt
```

- [ ] **Step 6: Write the failing buildinfo test**

Create `apps/server/internal/buildinfo/buildinfo_test.go`:

```go
package buildinfo

import "testing"

// An unstamped build (go test, go run) must say "dev" so a locally built
// binary can never be mistaken for a release in a log or health response.
func TestVersionDefaultsToDev(t *testing.T) {
	if Version != "dev" {
		t.Fatalf("Version = %q, want %q", Version, "dev")
	}
}
```

- [ ] **Step 7: Run it to verify it fails**

Run: `cd apps/server && go test ./internal/buildinfo/`
Expected: FAIL to compile with `undefined: Version`.

- [ ] **Step 8: Implement buildinfo**

Create `apps/server/internal/buildinfo/buildinfo.go`:

```go
// Package buildinfo is the one place the running binary's version lives.
//
// Version is stamped at link time (-X …/buildinfo.Version=<v>) by
// .goreleaser.yaml for releases and by scripts/build-artifacts.sh
// (VANTIGO_VERSION) for CI preview images; a plain `go build` leaves "dev".
// It is the same string the image is tagged with, and everything that names a
// version names this one: the boot log, /health responses and the
// OpenTelemetry resource.
package buildinfo

// Version is the build version, or "dev" when not stamped.
var Version = "dev"
```

- [ ] **Step 9: Verify everything passes**

Run: `cd apps/server && go test ./... && golangci-lint run && cd ../.. && bun test tools/toolchain && bun run toolchain:check`
Expected: `ok  github.com/vantigo-io/vantigo/server/internal/buildinfo`, `0 issues.`, 4 bun tests pass, `Toolchain checks passed (mise.toml agrees with global.json, .bun-version and apps/server/go.mod).`

Link-time stamping is verified end to end by the image smoke test in Task 12 (`SMOKE_EXPECT_VERSION`).

- [ ] **Step 10: Commit**

```bash
git add mise.toml package.json tools/toolchain apps/server/go.mod apps/server/.golangci.yml apps/server/internal/buildinfo
git commit -m "build(server): pin the Go toolchain and create the server module

Adds go and the release/lint tools to mise.toml, a toolchain check that
keeps apps/server/go.mod's go directive in step with the mise pin, and the
buildinfo package that carries the stamped version."
```

---

### Task 2: Configuration

**Files:**
- Create: `apps/server/internal/config/config.go`
- Test: `apps/server/internal/config/config_test.go`

**Interfaces:**
- Consumes: nothing.
- Produces:
  - `type Env string` with `config.Production`, `config.Development`
  - `type Branding struct { Title, LogoURL, SupportEmail, SupportPhone, SupportURL string }`
  - `type Config struct { Env Env; DatabaseURL, MigrationsDatabaseURL, AppOrigin, AppHostname, BasePath string; Port, TrustedProxyHops int; AllowInsecureTransport, CSPReportOnly bool; ShutdownTimeout time.Duration; LogLevel slog.Level; Branding Branding }`
  - `func (c *Config) IsDevelopment() bool`, `func (c *Config) EnforcesTransportSecurity() bool`
  - `func Load(env map[string]string) (*Config, error)`, `func FromOS() (*Config, error)`, `func NormalizeBasePath(raw string) string`

Environment contract for this sub-project:

| Variable | Default | Rule |
|---|---|---|
| `APP_ENV` | `production` | `production` or `development` |
| `DATABASE_URL` | — (required) | parseable by pgx; certificate-verified TLS when transport rules apply |
| `MIGRATIONS_DATABASE_URL` | `DATABASE_URL` | same rules when set |
| `APP_URL` | — (required) | absolute http(s) origin, no path/query/fragment/userinfo; https when transport rules apply |
| `APP_BASE_PATH` | root | `/seg/seg`, segments of `[A-Za-z0-9._~-]`, no `.`/`..` |
| `PORT` | `8080` | 1–65535 |
| `TRUSTED_PROXY_HOPS` | `0` | 0–10 |
| `ALLOW_INSECURE_TRANSPORT` | `0` | `0`/`1` |
| `CSP_REPORT_ONLY` | `0` | `0`/`1` |
| `SHUTDOWN_TIMEOUT` | `30s` | positive Go duration |
| `LOG_LEVEL` | `info` | `debug`, `info`, `warn`, `error` |
| `APP_TITLE`, `APP_SUPPORT_PHONE` | empty | free text, trimmed |
| `APP_LOGO_URL`, `APP_SUPPORT_URL` | empty | absolute http(s) URL or a path starting with `/` |
| `APP_SUPPORT_EMAIL` | empty | a plain address |

Transport rules apply when `APP_ENV=production` and `ALLOW_INSECURE_TRANSPORT` is not `1`.

- [ ] **Step 1: Write the failing tests**

Create `apps/server/internal/config/config_test.go`:

```go
package config

import (
	"log/slog"
	"strings"
	"testing"
	"time"
)

func validEnv() map[string]string {
	return map[string]string{
		"DATABASE_URL": "postgres://vantigo:secret@db.internal:5432/vantigo?sslmode=verify-full",
		"APP_URL":      "https://vantigo.example.com",
	}
}

// with returns a copy of env with the key/value pairs applied.
func with(env map[string]string, pairs ...string) map[string]string {
	out := make(map[string]string, len(env)+len(pairs)/2)
	for k, v := range env {
		out[k] = v
	}
	for i := 0; i+1 < len(pairs); i += 2 {
		out[pairs[i]] = pairs[i+1]
	}
	return out
}

func mustLoad(t *testing.T, env map[string]string) *Config {
	t.Helper()
	cfg, err := Load(env)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	return cfg
}

func loadError(t *testing.T, env map[string]string) string {
	t.Helper()
	_, err := Load(env)
	if err == nil {
		t.Fatal("Load succeeded, want an error")
	}
	return err.Error()
}

func TestLoad_MinimalProductionConfigGetsDefaults(t *testing.T) {
	cfg := mustLoad(t, validEnv())

	if cfg.Env != Production || cfg.IsDevelopment() {
		t.Errorf("Env = %q, want production", cfg.Env)
	}
	if !cfg.EnforcesTransportSecurity() {
		t.Error("EnforcesTransportSecurity = false, want true in production")
	}
	if cfg.MigrationsDatabaseURL != cfg.DatabaseURL {
		t.Errorf("MigrationsDatabaseURL = %q, want DATABASE_URL", cfg.MigrationsDatabaseURL)
	}
	if cfg.AppOrigin != "https://vantigo.example.com" || cfg.AppHostname != "vantigo.example.com" {
		t.Errorf("AppOrigin/AppHostname = %q/%q", cfg.AppOrigin, cfg.AppHostname)
	}
	if cfg.BasePath != "" || cfg.Port != 8080 || cfg.TrustedProxyHops != 0 {
		t.Errorf("BasePath/Port/TrustedProxyHops = %q/%d/%d", cfg.BasePath, cfg.Port, cfg.TrustedProxyHops)
	}
	if cfg.ShutdownTimeout != 30*time.Second || cfg.LogLevel != slog.LevelInfo {
		t.Errorf("ShutdownTimeout/LogLevel = %v/%v", cfg.ShutdownTimeout, cfg.LogLevel)
	}
	if cfg.AllowInsecureTransport || cfg.CSPReportOnly {
		t.Error("flags default to on, want off")
	}
}

func TestLoad_ReportsEveryProblemAtOnce(t *testing.T) {
	msg := loadError(t, map[string]string{
		"APP_URL":            "not a url",
		"PORT":               "0",
		"SHUTDOWN_TIMEOUT":   "soon",
		"TRUSTED_PROXY_HOPS": "-1",
	})

	if !strings.HasPrefix(msg, "invalid configuration:\n  ") {
		t.Errorf("error does not start with the heading: %q", msg)
	}
	for _, want := range []string{
		"DATABASE_URL: is required",
		"APP_URL: must be an absolute http or https URL",
		"PORT: must be an integer from 1 to 65535",
		"SHUTDOWN_TIMEOUT: must be a positive duration",
		"TRUSTED_PROXY_HOPS: must be an integer from 0 to 10",
	} {
		if !strings.Contains(msg, want) {
			t.Errorf("error is missing %q:\n%s", want, msg)
		}
	}
}

func TestLoad_AppURL(t *testing.T) {
	accepted := []struct{ raw, origin, hostname string }{
		{"https://Vantigo.Example.com/", "https://vantigo.example.com", "vantigo.example.com"},
		{"https://vantigo.example.com:8443", "https://vantigo.example.com:8443", "vantigo.example.com"},
		{"https://[2001:db8::1]:8443", "https://[2001:db8::1]:8443", "2001:db8::1"},
	}
	for _, tc := range accepted {
		t.Run(tc.raw, func(t *testing.T) {
			cfg := mustLoad(t, with(validEnv(), "APP_URL", tc.raw))
			if cfg.AppOrigin != tc.origin || cfg.AppHostname != tc.hostname {
				t.Errorf("got %q/%q, want %q/%q", cfg.AppOrigin, cfg.AppHostname, tc.origin, tc.hostname)
			}
		})
	}

	rejected := []struct{ raw, want string }{
		{"ftp://vantigo.example.com", "APP_URL: must be an absolute http or https URL"},
		{"vantigo.example.com", "APP_URL: must be an absolute http or https URL"},
		{"https://vantigo.example.com/app", "APP_URL: must be an origin only"},
		{"https://user:pw@vantigo.example.com", "APP_URL: must be an origin only"},
		{"https://vantigo.example.com/?x=1", "APP_URL: must be an origin only"},
	}
	for _, tc := range rejected {
		t.Run(tc.raw, func(t *testing.T) {
			if msg := loadError(t, with(validEnv(), "APP_URL", tc.raw)); !strings.Contains(msg, tc.want) {
				t.Errorf("error %q does not contain %q", msg, tc.want)
			}
		})
	}
}

func TestLoad_TransportRules(t *testing.T) {
	tests := []struct {
		name    string
		env     map[string]string
		wantErr string // empty: must load
	}{
		{"production requires https", with(validEnv(), "APP_URL", "http://vantigo.example.com"), "APP_URL: must use https"},
		{"sslmode=require does not authenticate the server", with(validEnv(), "DATABASE_URL", "postgres://v:s@db/v?sslmode=require"), "DATABASE_URL: must require certificate-verified TLS"},
		{"no sslmode means libpq's prefer", with(validEnv(), "DATABASE_URL", "postgres://v:s@db/v"), `"prefer"`},
		{"keyword form with verify-ca is accepted", with(validEnv(), "DATABASE_URL", "host=db user=v dbname=v sslmode=verify-ca"), ""},
		{"PGSSLMODE supplies a missing mode", with(validEnv(), "DATABASE_URL", "postgres://v:s@db/v", "PGSSLMODE", "verify-full"), ""},
		{"an insecure migrations URL is reported by name", with(validEnv(), "MIGRATIONS_DATABASE_URL", "postgres://o:s@db/v?sslmode=disable"), "MIGRATIONS_DATABASE_URL: must require certificate-verified TLS"},
		{"ALLOW_INSECURE_TRANSPORT accepts plaintext", with(validEnv(), "APP_URL", "http://localhost:8080", "DATABASE_URL", "postgres://v:s@db/v?sslmode=disable", "ALLOW_INSECURE_TRANSPORT", "1"), ""},
		{"development accepts plaintext", with(validEnv(), "APP_ENV", "development", "APP_URL", "http://localhost:8080", "DATABASE_URL", "postgres://v:s@db/v?sslmode=disable"), ""},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			_, err := Load(tc.env)
			switch {
			case tc.wantErr == "" && err != nil:
				t.Fatalf("Load: %v", err)
			case tc.wantErr != "" && (err == nil || !strings.Contains(err.Error(), tc.wantErr)):
				t.Fatalf("error = %v, want it to contain %q", err, tc.wantErr)
			}
		})
	}
}

func TestLoad_InvalidDatabaseURLNeverEchoesTheSecret(t *testing.T) {
	msg := loadError(t, with(validEnv(), "DATABASE_URL", "postgres://vantigo:hunter2@db/v?sslmode=bogus"))

	if !strings.Contains(msg, "DATABASE_URL: is not a valid PostgreSQL connection string") {
		t.Errorf("error = %q", msg)
	}
	if strings.Contains(msg, "hunter2") {
		t.Errorf("error leaks the password: %q", msg)
	}
}

func TestNormalizeBasePath(t *testing.T) {
	for raw, want := range map[string]string{
		"":             "",
		"/":            "",
		"vantigo":      "/vantigo",
		"/vantigo/":    "/vantigo",
		"  /a/b/  ":    "/a/b",
		"/erp/vantigo": "/erp/vantigo",
	} {
		if got := NormalizeBasePath(raw); got != want {
			t.Errorf("NormalizeBasePath(%q) = %q, want %q", raw, got, want)
		}
	}
}

func TestLoad_BasePath(t *testing.T) {
	if got := mustLoad(t, with(validEnv(), "APP_BASE_PATH", "vantigo/")).BasePath; got != "/vantigo" {
		t.Errorf("BasePath = %q, want /vantigo", got)
	}
	for _, raw := range []string{"/a b", "/../x", "/a//b", "/a?b", "/."} {
		if msg := loadError(t, with(validEnv(), "APP_BASE_PATH", raw)); !strings.Contains(msg, "APP_BASE_PATH: must be a path like /vantigo") {
			t.Errorf("APP_BASE_PATH=%q: error %q", raw, msg)
		}
	}
}

func TestLoad_FlagsAreStrict(t *testing.T) {
	if msg := loadError(t, with(validEnv(), "ALLOW_INSECURE_TRANSPORT", "true")); !strings.Contains(msg, `ALLOW_INSECURE_TRANSPORT: must be "0" or "1"`) {
		t.Errorf("error = %q", msg)
	}
	if !mustLoad(t, with(validEnv(), "CSP_REPORT_ONLY", "1")).CSPReportOnly {
		t.Error("CSP_REPORT_ONLY=1 did not enable report-only mode")
	}
}

func TestLoad_Branding(t *testing.T) {
	cfg := mustLoad(t, with(validEnv(),
		"APP_TITLE", "  Acme ERP  ",
		"APP_LOGO_URL", "/logo.svg",
		"APP_SUPPORT_EMAIL", "help@acme.test",
		"APP_SUPPORT_PHONE", " +47 123 45 678 ",
		"APP_SUPPORT_URL", "https://support.acme.test",
	))
	want := Branding{Title: "Acme ERP", LogoURL: "/logo.svg", SupportEmail: "help@acme.test", SupportPhone: "+47 123 45 678", SupportURL: "https://support.acme.test"}
	if cfg.Branding != want {
		t.Errorf("Branding = %+v, want %+v", cfg.Branding, want)
	}

	msg := loadError(t, with(validEnv(),
		"APP_LOGO_URL", "javascript:alert(1)",
		"APP_SUPPORT_URL", "//evil.example",
		"APP_SUPPORT_EMAIL", "Help <help@acme.test>",
	))
	for _, want := range []string{
		"APP_LOGO_URL: must be an absolute http or https URL or a path starting with /",
		"APP_SUPPORT_URL: must be an absolute http or https URL or a path starting with /",
		"APP_SUPPORT_EMAIL: must be a plain email address",
	} {
		if !strings.Contains(msg, want) {
			t.Errorf("error is missing %q:\n%s", want, msg)
		}
	}
}

func TestLoad_EnvAndLogLevel(t *testing.T) {
	if got := mustLoad(t, with(validEnv(), "LOG_LEVEL", "debug")).LogLevel; got != slog.LevelDebug {
		t.Errorf("LogLevel = %v, want debug", got)
	}
	msg := loadError(t, with(validEnv(), "LOG_LEVEL", "verbose", "APP_ENV", "staging"))
	for _, want := range []string{
		`LOG_LEVEL: must be one of "debug", "info", "warn", "error"`,
		`APP_ENV: must be "production" or "development"`,
	} {
		if !strings.Contains(msg, want) {
			t.Errorf("error is missing %q:\n%s", want, msg)
		}
	}
}
```

- [ ] **Step 2: Run the tests to verify they fail**

Run: `cd apps/server && go test ./internal/config/`
Expected: FAIL to compile with `undefined: Load`, `undefined: Config`, and similar.

- [ ] **Step 3: Implement the loader**

Create `apps/server/internal/config/config.go`:

```go
// Package config loads and validates the process configuration from
// environment variables. It is parsed once at startup and reports every
// problem at once, so a misconfigured container fails its first boot with the
// complete list instead of one restart per mistake.
//
// Add new settings here, never by reading os.Getenv at a call site.
package config

import (
	"fmt"
	"log/slog"
	"net/mail"
	"net/url"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/jackc/pgx/v5/pgconn"
)

// Env is the deployment environment. Only development relaxes anything.
type Env string

const (
	Production  Env = "production"
	Development Env = "development"
)

// Branding is the whitelabeling the SPA index document is templated with.
type Branding struct {
	Title        string
	LogoURL      string
	SupportEmail string
	SupportPhone string
	SupportURL   string
}

// Config is the validated process configuration. Load populates every field;
// there is no partial state.
type Config struct {
	Env Env
	// DatabaseURL is the runtime connection (the least-privilege role).
	DatabaseURL string
	// MigrationsDatabaseURL is the connection `migrate` uses: the owner role
	// where a deployment separates the two, otherwise DatabaseURL.
	MigrationsDatabaseURL string
	// AppOrigin is APP_URL as scheme://host[:port], lower-case, no trailing slash.
	AppOrigin string
	// AppHostname is AppOrigin's host without port or IPv6 brackets.
	AppHostname string
	// BasePath is "" at the domain root, otherwise "/prefix" with no trailing slash.
	BasePath               string
	Port                   int
	TrustedProxyHops       int
	AllowInsecureTransport bool
	CSPReportOnly          bool
	ShutdownTimeout        time.Duration
	LogLevel               slog.Level
	Branding               Branding
}

// IsDevelopment reports whether APP_ENV=development.
func (c *Config) IsDevelopment() bool { return c.Env == Development }

// EnforcesTransportSecurity reports whether the fail-closed transport rules
// apply: always outside development, unless the operator knowingly set
// ALLOW_INSECURE_TRANSPORT=1.
func (c *Config) EnforcesTransportSecurity() bool {
	return c.Env == Production && !c.AllowInsecureTransport
}

// FromOS loads configuration from the process environment.
func FromOS() (*Config, error) {
	env := make(map[string]string)
	for _, kv := range os.Environ() {
		if k, v, ok := strings.Cut(kv, "="); ok {
			env[k] = v
		}
	}
	return Load(env)
}

// Load parses and validates configuration from a plain map, the shape both a
// real environment and a test fixture share. It reports every problem in one
// error.
func Load(env map[string]string) (*Config, error) {
	var p problems
	c := &Config{}

	switch env["APP_ENV"] {
	case "", string(Production):
		c.Env = Production
	case string(Development):
		c.Env = Development
	default:
		p.add("APP_ENV", `must be "production" or "development"`)
		c.Env = Production
	}

	c.AllowInsecureTransport = flag(&p, env, "ALLOW_INSECURE_TRANSPORT")
	c.CSPReportOnly = flag(&p, env, "CSP_REPORT_ONLY")

	c.DatabaseURL = databaseURL(&p, env, "DATABASE_URL", true)
	c.MigrationsDatabaseURL = databaseURL(&p, env, "MIGRATIONS_DATABASE_URL", false)
	migrationsURLSet := c.MigrationsDatabaseURL != ""
	if !migrationsURLSet {
		c.MigrationsDatabaseURL = c.DatabaseURL
	}

	c.AppOrigin, c.AppHostname = appOrigin(&p, env)
	c.BasePath = basePath(&p, env)
	c.Port = integer(&p, env, "PORT", 8080, 1, 65535)
	c.TrustedProxyHops = integer(&p, env, "TRUSTED_PROXY_HOPS", 0, 0, 10)
	c.ShutdownTimeout = duration(&p, env, "SHUTDOWN_TIMEOUT", 30*time.Second)
	c.LogLevel = logLevel(&p, env)
	c.Branding = branding(&p, env)

	if c.EnforcesTransportSecurity() {
		if c.AppOrigin != "" && !strings.HasPrefix(c.AppOrigin, "https://") {
			p.add("APP_URL", "must use https outside development; set ALLOW_INSECURE_TRANSPORT=1 to knowingly accept plaintext (local and evaluation use only)")
		}
		if c.DatabaseURL != "" {
			requireVerifiedTLS(&p, env, "DATABASE_URL", c.DatabaseURL)
		}
		if migrationsURLSet {
			requireVerifiedTLS(&p, env, "MIGRATIONS_DATABASE_URL", c.MigrationsDatabaseURL)
		}
	}

	if len(p) > 0 {
		return nil, fmt.Errorf("invalid configuration:\n  %s", strings.Join(p, "\n  "))
	}
	return c, nil
}

// NormalizeBasePath returns raw as "/prefix" (leading slash, no trailing
// slash), or "" when it is empty or "/" — serve at the domain root. Mirrors
// the .NET AppBasePathOptions.Normalized.
func NormalizeBasePath(raw string) string {
	v := strings.Trim(strings.TrimSpace(raw), "/")
	if v == "" {
		return ""
	}
	return "/" + v
}

// problems accumulates every validation failure instead of stopping at the first.
type problems []string

func (p *problems) add(field, format string, args ...any) {
	*p = append(*p, field+": "+fmt.Sprintf(format, args...))
}

func databaseURL(p *problems, env map[string]string, field string, required bool) string {
	v := env[field]
	if v == "" {
		if required {
			p.add(field, "is required")
		}
		return ""
	}
	// The parser's own error is not echoed: it can quote the connection string.
	if _, err := pgconn.ParseConfig(v); err != nil {
		p.add(field, "is not a valid PostgreSQL connection string")
		return ""
	}
	return v
}

// requireVerifiedTLS rejects any sslmode that does not authenticate the
// server. libpq's default, "prefer", silently falls back to plaintext and
// never checks a certificate even when TLS is used.
func requireVerifiedTLS(p *problems, env map[string]string, field, dsn string) {
	switch mode := sslMode(dsn, env["PGSSLMODE"]); mode {
	case "verify-full", "verify-ca":
	default:
		p.add(field, "must require certificate-verified TLS outside development (sslmode=verify-full, or verify-ca when the server certificate does not name the host); %q does not authenticate the server. Set ALLOW_INSECURE_TRANSPORT=1 to knowingly accept an unauthenticated database connection (local and evaluation use only)", mode)
	}
}

// sslMode extracts the sslmode a connection string requests, in URL
// (postgres://…?sslmode=…) or keyword/value (host=… sslmode=…) form, falling
// back to PGSSLMODE and then libpq's default. Anything unrecognised reads as
// the default, which the caller rejects: the parser fails closed.
func sslMode(dsn, pgsslmode string) string {
	if strings.HasPrefix(dsn, "postgres://") || strings.HasPrefix(dsn, "postgresql://") {
		if u, err := url.Parse(dsn); err == nil {
			if m := u.Query().Get("sslmode"); m != "" {
				return strings.ToLower(m)
			}
		}
	} else {
		for _, field := range strings.Fields(dsn) {
			if k, v, ok := strings.Cut(field, "="); ok && k == "sslmode" {
				return strings.ToLower(strings.Trim(v, "'"))
			}
		}
	}
	if pgsslmode != "" {
		return strings.ToLower(pgsslmode)
	}
	return "prefer"
}

func appOrigin(p *problems, env map[string]string) (origin, hostname string) {
	v := env["APP_URL"]
	if v == "" {
		p.add("APP_URL", "is required")
		return "", ""
	}
	u, err := url.Parse(v)
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Hostname() == "" {
		p.add("APP_URL", "must be an absolute http or https URL")
		return "", ""
	}
	if u.User != nil || (u.Path != "" && u.Path != "/") || u.RawQuery != "" || u.Fragment != "" {
		p.add("APP_URL", "must be an origin only (scheme, host and optional port); put a path prefix in APP_BASE_PATH")
		return "", ""
	}
	return strings.ToLower(u.Scheme + "://" + u.Host), strings.ToLower(u.Hostname())
}

func basePath(p *problems, env map[string]string) string {
	v := NormalizeBasePath(env["APP_BASE_PATH"])
	if v == "" {
		return ""
	}
	for _, seg := range strings.Split(v[1:], "/") {
		if seg == "" || seg == "." || seg == ".." || strings.IndexFunc(seg, invalidPathRune) >= 0 {
			p.add("APP_BASE_PATH", "must be a path like /vantigo made of letters, digits, '.', '_', '~' and '-'")
			return ""
		}
	}
	return v
}

func invalidPathRune(r rune) bool {
	switch {
	case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9':
		return false
	case r == '.' || r == '_' || r == '~' || r == '-':
		return false
	}
	return true
}

func integer(p *problems, env map[string]string, field string, def, min, max int) int {
	v := env[field]
	if v == "" {
		return def
	}
	n, err := strconv.Atoi(v)
	if err != nil || n < min || n > max {
		p.add(field, "must be an integer from %d to %d", min, max)
		return def
	}
	return n
}

func duration(p *problems, env map[string]string, field string, def time.Duration) time.Duration {
	v := env[field]
	if v == "" {
		return def
	}
	d, err := time.ParseDuration(v)
	if err != nil || d <= 0 {
		p.add(field, "must be a positive duration such as 30s")
		return def
	}
	return d
}

// flag reads a strict "0"/"1" switch. Anything else is rejected rather than
// guessed at: the fail-safe default stays off.
func flag(p *problems, env map[string]string, field string) bool {
	switch env[field] {
	case "", "0":
		return false
	case "1":
		return true
	default:
		p.add(field, `must be "0" or "1"`)
		return false
	}
}

func logLevel(p *problems, env map[string]string) slog.Level {
	switch strings.ToLower(env["LOG_LEVEL"]) {
	case "", "info":
		return slog.LevelInfo
	case "debug":
		return slog.LevelDebug
	case "warn":
		return slog.LevelWarn
	case "error":
		return slog.LevelError
	default:
		p.add("LOG_LEVEL", `must be one of "debug", "info", "warn", "error"`)
		return slog.LevelInfo
	}
}

func branding(p *problems, env map[string]string) Branding {
	b := Branding{
		Title:        strings.TrimSpace(env["APP_TITLE"]),
		SupportPhone: strings.TrimSpace(env["APP_SUPPORT_PHONE"]),
		LogoURL:      linkField(p, env, "APP_LOGO_URL"),
		SupportURL:   linkField(p, env, "APP_SUPPORT_URL"),
	}
	if v := strings.TrimSpace(env["APP_SUPPORT_EMAIL"]); v != "" {
		if a, err := mail.ParseAddress(v); err != nil || a.Address != v {
			p.add("APP_SUPPORT_EMAIL", "must be a plain email address such as support@example.com")
		} else {
			b.SupportEmail = v
		}
	}
	return b
}

// linkField accepts an absolute http(s) URL or a same-origin path. Anything
// else (javascript:, protocol-relative //host) would end up in an href or src
// the SPA renders.
func linkField(p *problems, env map[string]string, field string) string {
	v := strings.TrimSpace(env[field])
	if v == "" {
		return ""
	}
	if strings.HasPrefix(v, "/") && !strings.HasPrefix(v, "//") {
		return v
	}
	u, err := url.Parse(v)
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" {
		p.add(field, "must be an absolute http or https URL or a path starting with /")
		return ""
	}
	return v
}
```

- [ ] **Step 4: Add the dependency**

Run: `cd apps/server && go get github.com/jackc/pgx/v5@v5.10.0 && go mod tidy`
Expected: `go.mod` gains `require github.com/jackc/pgx/v5 v5.10.0`, and `go.sum` is created.

- [ ] **Step 5: Run the tests to verify they pass**

Run: `cd apps/server && go test ./internal/config/ -v -count=1 && golangci-lint run`
Expected: every `TestLoad_*` and `TestNormalizeBasePath` PASS; `0 issues.`

- [ ] **Step 6: Commit**

```bash
git add apps/server/go.mod apps/server/go.sum apps/server/internal/config
git commit -m "feat(server): load and validate configuration from the environment

Parses every setting the skeleton needs, reports all problems at once, and
fails closed on plaintext transport (http APP_URL, unverified PostgreSQL
TLS) outside development unless ALLOW_INSECURE_TRANSPORT=1."
```

---

### Task 3: HTTP plumbing (`httpx`)

**Files:**
- Create: `apps/server/internal/httpx/problem.go`, `trace.go`, `recover.go`, `log.go`, `forwarded.go`, `basepath.go`, `chain.go`
- Test: `apps/server/internal/httpx/problem_test.go`, `trace_test.go`, `recover_test.go`, `log_test.go`, `forwarded_test.go`, `basepath_test.go`, `chain_test.go`

**Interfaces:**
- Consumes: nothing from earlier tasks.
- Produces:
  - `type Problem struct { Type, Title string; Status int; Detail, TraceID string }` (JSON `type,title,status,detail,traceId`)
  - `func WriteProblem(w http.ResponseWriter, r *http.Request, status int, detail string)`
  - `func WriteError(w http.ResponseWriter, r *http.Request, err error)` — PostgreSQL `23505`/`23P01` → 409, `*http.MaxBytesError` → 413, a cancelled caller → nothing written, anything else → 500. Logs through `slog.Default()`, which the composition root installs.
  - `func NotFound(w http.ResponseWriter, r *http.Request)`
  - consts `UnexpectedErrorDetail`, `ConflictDetail`, `TooLargeDetail`
  - `func RequestID(next http.Handler) http.Handler`, `func TraceID(r *http.Request) string`
  - `func Recover(logger *slog.Logger) func(http.Handler) http.Handler`
  - `func RequestLog(logger *slog.Logger) func(http.Handler) http.Handler`
  - `func Forwarded(trustedHops int) func(http.Handler) http.Handler`, `func ClientIP(r *http.Request) string`, `func Scheme(r *http.Request) string`
  - `func StripBasePath(base string) func(http.Handler) http.Handler`
  - `func Chain(h http.Handler, middleware ...func(http.Handler) http.Handler) http.Handler` — the first middleware listed is the outermost

- [ ] **Step 1: Write the failing tests**

Create `apps/server/internal/httpx/problem_test.go`:

```go
package httpx

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/jackc/pgx/v5/pgconn"
)

func decodeProblem(t *testing.T, rec *httptest.ResponseRecorder) Problem {
	t.Helper()
	if ct := rec.Header().Get("Content-Type"); ct != "application/problem+json" {
		t.Fatalf("Content-Type = %q, want application/problem+json", ct)
	}
	var p Problem
	if err := json.Unmarshal(rec.Body.Bytes(), &p); err != nil {
		t.Fatalf("decode problem: %v (body %q)", err, rec.Body.String())
	}
	return p
}

// captureDefaultLog swaps slog.Default for a buffer for one test.
func captureDefaultLog(t *testing.T) *bytes.Buffer {
	t.Helper()
	var buf bytes.Buffer
	previous := slog.Default()
	slog.SetDefault(slog.New(slog.NewJSONHandler(&buf, &slog.HandlerOptions{Level: slog.LevelDebug})))
	t.Cleanup(func() { slog.SetDefault(previous) })
	return &buf
}

func TestWriteProblem_WritesAnRFC7807Document(t *testing.T) {
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/api/v1/nope", nil)
	RequestID(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		req = r
		WriteProblem(w, r, http.StatusNotFound, "No such customer.")
	})).ServeHTTP(rec, req)

	p := decodeProblem(t, rec)
	want := Problem{
		Type:    "https://tools.ietf.org/html/rfc9110#section-15.5.5",
		Title:   "Not Found",
		Status:  http.StatusNotFound,
		Detail:  "No such customer.",
		TraceID: TraceID(req),
	}
	if p != want {
		t.Errorf("problem = %+v, want %+v", p, want)
	}
	if rec.Code != http.StatusNotFound || rec.Header().Get("Cache-Control") != "no-store" {
		t.Errorf("status/cache = %d/%q", rec.Code, rec.Header().Get("Cache-Control"))
	}
}

func TestWriteProblem_UnknownStatusUsesAboutBlankAndOmitsEmptyDetail(t *testing.T) {
	rec := httptest.NewRecorder()
	WriteProblem(rec, httptest.NewRequest(http.MethodGet, "/", nil), http.StatusTeapot, "")

	p := decodeProblem(t, rec)
	if p.Type != "about:blank" || p.Title != "I'm a teapot" {
		t.Errorf("type/title = %q/%q", p.Type, p.Title)
	}
	if strings.Contains(rec.Body.String(), `"detail"`) {
		t.Errorf("empty detail was serialised: %s", rec.Body.String())
	}
}

func TestNotFound(t *testing.T) {
	rec := httptest.NewRecorder()
	NotFound(rec, httptest.NewRequest(http.MethodGet, "/api/anything", nil))
	if p := decodeProblem(t, rec); p.Status != http.StatusNotFound {
		t.Errorf("status = %d", p.Status)
	}
}

func TestWriteError_MapsConstraintViolationsToConflict(t *testing.T) {
	captureDefaultLog(t)
	for _, code := range []string{"23505", "23P01"} {
		t.Run(code, func(t *testing.T) {
			rec := httptest.NewRecorder()
			err := &pgconn.PgError{Code: code, ConstraintName: "users_email_key", Message: "duplicate key value"}
			WriteError(rec, httptest.NewRequest(http.MethodPost, "/api/x", nil), err)

			p := decodeProblem(t, rec)
			if p.Status != http.StatusConflict || p.Detail != ConflictDetail {
				t.Errorf("problem = %+v", p)
			}
			if strings.Contains(rec.Body.String(), "users_email_key") {
				t.Errorf("response leaks the constraint name: %s", rec.Body.String())
			}
		})
	}
}

func TestWriteError_MapsAnOversizedBodyTo413(t *testing.T) {
	rec := httptest.NewRecorder()
	WriteError(rec, httptest.NewRequest(http.MethodPost, "/api/x", nil), &http.MaxBytesError{Limit: 10})
	if p := decodeProblem(t, rec); p.Status != http.StatusRequestEntityTooLarge || p.Detail != TooLargeDetail {
		t.Errorf("problem = %+v", p)
	}
}

func TestWriteError_SanitisesUnexpectedErrorsButLogsThem(t *testing.T) {
	logs := captureDefaultLog(t)
	rec := httptest.NewRecorder()
	WriteError(rec, httptest.NewRequest(http.MethodGet, "/api/x", nil), errors.New("dial tcp: password hunter2 rejected"))

	p := decodeProblem(t, rec)
	if p.Status != http.StatusInternalServerError || p.Detail != UnexpectedErrorDetail {
		t.Errorf("problem = %+v", p)
	}
	if strings.Contains(rec.Body.String(), "hunter2") {
		t.Errorf("response leaks the error: %s", rec.Body.String())
	}
	if !strings.Contains(logs.String(), "hunter2") {
		t.Errorf("the error was not logged: %s", logs.String())
	}
}

func TestWriteError_WritesNothingWhenTheCallerIsGone(t *testing.T) {
	captureDefaultLog(t)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	rec := httptest.NewRecorder()
	WriteError(rec, httptest.NewRequest(http.MethodGet, "/api/x", nil).WithContext(ctx), context.Canceled)

	if rec.Body.Len() != 0 || rec.Header().Get("Content-Type") != "" {
		t.Errorf("wrote a response to a caller that left: %d %q", rec.Code, rec.Body.String())
	}
}
```

Create `apps/server/internal/httpx/trace_test.go`:

```go
package httpx

import (
	"context"
	"net/http"
	"net/http/httptest"
	"regexp"
	"testing"

	"go.opentelemetry.io/otel/trace"
)

var traceIDShape = regexp.MustCompile(`^[0-9a-f]{32}$`)

func TestRequestID_GivesEveryRequestADistinctTraceShapedID(t *testing.T) {
	var ids []string
	h := RequestID(http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) {
		ids = append(ids, TraceID(r))
	}))
	h.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest(http.MethodGet, "/", nil))
	h.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest(http.MethodGet, "/", nil))

	for _, id := range ids {
		if !traceIDShape.MatchString(id) {
			t.Errorf("id %q is not 32 lower-case hex characters", id)
		}
	}
	if ids[0] == ids[1] {
		t.Error("two requests got the same id")
	}
}

func TestTraceID_PrefersTheActiveSpan(t *testing.T) {
	traceID := trace.TraceID{0x0a, 0x0b, 0x0c, 0x0d, 0x0e, 0x0f, 0x10, 0x11, 0x12, 0x13, 0x14, 0x15, 0x16, 0x17, 0x18, 0x19}
	sc := trace.NewSpanContext(trace.SpanContextConfig{TraceID: traceID, SpanID: trace.SpanID{1}})

	var got string
	RequestID(http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) {
		got = TraceID(r)
	})).ServeHTTP(httptest.NewRecorder(), httptest.NewRequest(http.MethodGet, "/", nil).
		WithContext(trace.ContextWithSpanContext(context.Background(), sc)))

	if got != traceID.String() {
		t.Errorf("TraceID = %q, want the span's %q", got, traceID.String())
	}
}

func TestTraceID_EmptyWithoutASpanOrRequestID(t *testing.T) {
	if got := TraceID(httptest.NewRequest(http.MethodGet, "/", nil)); got != "" {
		t.Errorf("TraceID = %q, want empty", got)
	}
}
```

Create `apps/server/internal/httpx/recover_test.go`:

```go
package httpx

import (
	"bytes"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestRecover_TurnsAPanicIntoASanitisedProblem(t *testing.T) {
	var logs bytes.Buffer
	logger := slog.New(slog.NewJSONHandler(&logs, nil))
	h := Chain(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
		panic("database password is hunter2")
	}), RequestID, Recover(logger))

	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/api/x", nil))

	p := decodeProblem(t, rec)
	if p.Status != http.StatusInternalServerError || p.Detail != UnexpectedErrorDetail {
		t.Errorf("problem = %+v", p)
	}
	if strings.Contains(rec.Body.String(), "hunter2") {
		t.Errorf("response leaks the panic value: %s", rec.Body.String())
	}
	for _, want := range []string{"hunter2", "goroutine", p.TraceID} {
		if !strings.Contains(logs.String(), want) {
			t.Errorf("log is missing %q: %s", want, logs.String())
		}
	}
}

func TestRecover_LetsErrAbortHandlerThrough(t *testing.T) {
	h := Recover(slog.New(slog.DiscardHandler))(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
		panic(http.ErrAbortHandler)
	}))
	defer func() {
		if v := recover(); v != http.ErrAbortHandler {
			t.Fatalf("recovered %v, want http.ErrAbortHandler", v)
		}
	}()
	h.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest(http.MethodGet, "/", nil))
	t.Fatal("ServeHTTP returned; the ErrAbortHandler panic must propagate to net/http")
}
```

Create `apps/server/internal/httpx/log_test.go`:

```go
package httpx

import (
	"bytes"
	"encoding/json"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func logOne(t *testing.T, h http.Handler, req *http.Request) (map[string]any, string) {
	t.Helper()
	var logs bytes.Buffer
	logger := slog.New(slog.NewJSONHandler(&logs, &slog.HandlerOptions{Level: slog.LevelDebug}))
	Chain(h, RequestID, RequestLog(logger)).ServeHTTP(httptest.NewRecorder(), req)

	var entry map[string]any
	if err := json.Unmarshal(logs.Bytes(), &entry); err != nil {
		t.Fatalf("decode log line: %v (%q)", err, logs.String())
	}
	return entry, logs.String()
}

func TestRequestLog_RecordsTheOutcomeWithoutTheQueryString(t *testing.T) {
	h := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusCreated)
		_, _ = w.Write([]byte("created"))
	})
	entry, raw := logOne(t, h, httptest.NewRequest(http.MethodPost, "/api/v1/invitations/accept?token=secret-token", nil))

	if entry["msg"] != "http request" || entry["level"] != "INFO" {
		t.Errorf("msg/level = %v/%v", entry["msg"], entry["level"])
	}
	if entry["method"] != "POST" || entry["path"] != "/api/v1/invitations/accept" {
		t.Errorf("method/path = %v/%v", entry["method"], entry["path"])
	}
	if entry["status"] != float64(http.StatusCreated) || entry["bytes"] != float64(len("created")) {
		t.Errorf("status/bytes = %v/%v", entry["status"], entry["bytes"])
	}
	if id, _ := entry["trace_id"].(string); !traceIDShape.MatchString(id) {
		t.Errorf("trace_id = %v", entry["trace_id"])
	}
	// Query strings carry invitation and reset tokens; they never reach a log.
	if strings.Contains(raw, "secret-token") {
		t.Errorf("log contains the query string: %s", raw)
	}
}

func TestRequestLog_ImplicitOKIsRecordedAs200(t *testing.T) {
	h := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { _, _ = w.Write([]byte("ok")) })
	if entry, _ := logOne(t, h, httptest.NewRequest(http.MethodGet, "/", nil)); entry["status"] != float64(http.StatusOK) {
		t.Errorf("status = %v, want 200", entry["status"])
	}
}

func TestRequestLog_HealthProbesLogAtDebug(t *testing.T) {
	h := http.HandlerFunc(func(http.ResponseWriter, *http.Request) {})
	if entry, _ := logOne(t, h, httptest.NewRequest(http.MethodGet, "/health/ready", nil)); entry["level"] != "DEBUG" {
		t.Errorf("level = %v, want DEBUG", entry["level"])
	}
}

func TestRequestLog_KeepsTheResponseControllerWorking(t *testing.T) {
	var flushErr error
	h := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		flushErr = http.NewResponseController(w).Flush()
	})
	logOne(t, h, httptest.NewRequest(http.MethodGet, "/", nil))
	if flushErr != nil {
		t.Errorf("Flush through the log wrapper: %v", flushErr)
	}
}
```

Create `apps/server/internal/httpx/forwarded_test.go`:

```go
package httpx

import (
	"crypto/tls"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestForwarded(t *testing.T) {
	tests := []struct {
		name       string
		hops       int
		remote     string
		tls        bool
		xff, proto []string
		wantIP     string
		wantScheme string
	}{
		{"no trusted proxies ignores the headers", 0, "192.0.2.10:5555", false, []string{"203.0.113.7"}, []string{"https"}, "192.0.2.10", "http"},
		{"one hop takes the rightmost entry", 1, "10.0.0.2:5555", false, []string{"198.51.100.1, 203.0.113.7"}, []string{"https"}, "203.0.113.7", "https"},
		{"two hops take the second from the right", 2, "10.0.0.3:5555", false, []string{"203.0.113.7, 10.0.0.2"}, nil, "203.0.113.7", "http"},
		{"repeated header lines are one list", 2, "10.0.0.3:5555", false, []string{"203.0.113.7", "10.0.0.2"}, nil, "203.0.113.7", "http"},
		{"fewer entries than hops falls back to the peer", 2, "10.0.0.3:5555", false, []string{"203.0.113.7"}, nil, "10.0.0.3", "http"},
		{"a malformed entry falls back to the peer", 1, "10.0.0.2:5555", false, []string{"not-an-ip"}, nil, "10.0.0.2", "http"},
		{"bracketed IPv6 with a port", 1, "10.0.0.2:5555", false, []string{"[2001:db8::1]:443"}, nil, "2001:db8::1", "http"},
		{"an unknown proto is ignored", 1, "10.0.0.2:5555", false, nil, []string{"gopher"}, "10.0.0.2", "http"},
		{"the last proto entry wins", 1, "10.0.0.2:5555", false, nil, []string{"http, https"}, "10.0.0.2", "https"},
		{"TLS on the connection is https", 0, "192.0.2.10:5555", true, nil, nil, "192.0.2.10", "https"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodGet, "/", nil)
			req.RemoteAddr = tc.remote
			if tc.tls {
				req.TLS = &tls.ConnectionState{}
			}
			for _, v := range tc.xff {
				req.Header.Add("X-Forwarded-For", v)
			}
			for _, v := range tc.proto {
				req.Header.Add("X-Forwarded-Proto", v)
			}

			var ip, scheme string
			Forwarded(tc.hops)(http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) {
				ip, scheme = ClientIP(r), Scheme(r)
			})).ServeHTTP(httptest.NewRecorder(), req)

			if ip != tc.wantIP || scheme != tc.wantScheme {
				t.Errorf("ClientIP/Scheme = %q/%q, want %q/%q", ip, scheme, tc.wantIP, tc.wantScheme)
			}
		})
	}
}

func TestClientIPAndSchemeWithoutTheMiddleware(t *testing.T) {
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.RemoteAddr = "192.0.2.10:5555"
	req.Header.Set("X-Forwarded-For", "203.0.113.7")
	if ClientIP(req) != "192.0.2.10" || Scheme(req) != "http" {
		t.Errorf("ClientIP/Scheme = %q/%q", ClientIP(req), Scheme(req))
	}
}
```

Create `apps/server/internal/httpx/basepath_test.go`:

```go
package httpx

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestStripBasePath(t *testing.T) {
	tests := []struct{ base, path, want string }{
		{"", "/api/x", "/api/x"},
		{"/crm", "/crm", "/"},
		{"/crm", "/crm/", "/"},
		{"/crm", "/crm/api/v1/x", "/api/v1/x"},
		{"/crm", "/crmx/api", "/crmx/api"},
		{"/crm", "/health/ready", "/health/ready"},
		{"/erp/crm", "/erp/crm/assets/a.js", "/assets/a.js"},
	}
	for _, tc := range tests {
		t.Run(tc.base+" "+tc.path, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodGet, tc.path, nil)
			var got string
			StripBasePath(tc.base)(http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) {
				got = r.URL.Path
			})).ServeHTTP(httptest.NewRecorder(), req)

			if got != tc.want {
				t.Errorf("path = %q, want %q", got, tc.want)
			}
			if req.URL.Path != tc.path {
				t.Errorf("the caller's request was mutated to %q", req.URL.Path)
			}
		})
	}
}
```

Create `apps/server/internal/httpx/chain_test.go`:

```go
package httpx

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestChain_FirstMiddlewareIsOutermost(t *testing.T) {
	var order []string
	mark := func(name string) func(http.Handler) http.Handler {
		return func(next http.Handler) http.Handler {
			return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				order = append(order, name)
				next.ServeHTTP(w, r)
			})
		}
	}
	h := Chain(http.HandlerFunc(func(http.ResponseWriter, *http.Request) { order = append(order, "handler") }),
		mark("outer"), mark("inner"))
	h.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest(http.MethodGet, "/", nil))

	if got := strings.Join(order, ","); got != "outer,inner,handler" {
		t.Errorf("order = %s", got)
	}
}
```

- [ ] **Step 2: Run the tests to verify they fail**

Run: `cd apps/server && go test ./internal/httpx/`
Expected: FAIL to compile with `undefined: Problem`, `undefined: RequestID`, `undefined: Forwarded`, and similar.

- [ ] **Step 3: Implement problems**

Create `apps/server/internal/httpx/problem.go`:

```go
// Package httpx holds the HTTP plumbing every handler shares: RFC 7807
// problem responses, request and trace ids, panic recovery, request logging,
// forwarded-header trust and base-path mounting.
package httpx

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"

	"github.com/jackc/pgx/v5/pgconn"
)

// The only details a failed request can carry. Nothing from an error ever
// reaches a caller; the trace id correlates the response with the log line
// that has the real cause.
const (
	UnexpectedErrorDetail = "The request could not be completed. Quote the trace id when reporting this problem."
	ConflictDetail        = "The request conflicts with data that already exists. Verify the values and try again."
	TooLargeDetail        = "The request body is too large."
)

// Problem is an RFC 7807 problem document in the shape the frontend API
// client parses (packages/frontend-api-client).
type Problem struct {
	Type    string `json:"type"`
	Title   string `json:"title"`
	Status  int    `json:"status"`
	Detail  string `json:"detail,omitempty"`
	TraceID string `json:"traceId"`
}

// problemTypes are ASP.NET Core's defaults, so responses keep the shape the
// .NET host produced.
var problemTypes = map[int]struct{ typ, title string }{
	http.StatusBadRequest:            {"https://tools.ietf.org/html/rfc9110#section-15.5.1", "Bad Request"},
	http.StatusUnauthorized:          {"https://tools.ietf.org/html/rfc9110#section-15.5.2", "Unauthorized"},
	http.StatusForbidden:             {"https://tools.ietf.org/html/rfc9110#section-15.5.4", "Forbidden"},
	http.StatusNotFound:              {"https://tools.ietf.org/html/rfc9110#section-15.5.5", "Not Found"},
	http.StatusMethodNotAllowed:      {"https://tools.ietf.org/html/rfc9110#section-15.5.6", "Method Not Allowed"},
	http.StatusConflict:              {"https://tools.ietf.org/html/rfc9110#section-15.5.10", "Conflict"},
	http.StatusRequestEntityTooLarge: {"https://tools.ietf.org/html/rfc9110#section-15.5.14", "Content Too Large"},
	http.StatusUnprocessableEntity:   {"https://tools.ietf.org/html/rfc9110#section-15.5.21", "Unprocessable Entity"},
	http.StatusTooManyRequests:       {"https://tools.ietf.org/html/rfc6585#section-4", "Too Many Requests"},
	http.StatusInternalServerError:   {"https://tools.ietf.org/html/rfc9110#section-15.6.1", "An error occurred while processing your request."},
	http.StatusServiceUnavailable:    {"https://tools.ietf.org/html/rfc9110#section-15.6.4", "Service Unavailable"},
}

// WriteProblem writes a problem response for status. detail may be empty.
func WriteProblem(w http.ResponseWriter, r *http.Request, status int, detail string) {
	meta, ok := problemTypes[status]
	if !ok {
		meta.typ, meta.title = "about:blank", http.StatusText(status)
	}
	w.Header().Set("Content-Type", "application/problem+json")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(Problem{
		Type:    meta.typ,
		Title:   meta.title,
		Status:  status,
		Detail:  detail,
		TraceID: TraceID(r),
	})
}

// NotFound is the /api catch-all: an unknown API path answers 404 and never
// falls through to the SPA's index.html.
func NotFound(w http.ResponseWriter, r *http.Request) {
	WriteProblem(w, r, http.StatusNotFound, "")
}

// WriteError turns a handler error into a sanitised problem. Unique and
// exclusion violations are the database backstop behind a handler's friendly
// pre-check — two concurrent requests can both pass it — so the loser gets an
// expected 409, not a server fault.
func WriteError(w http.ResponseWriter, r *http.Request, err error) {
	ctx := r.Context()
	logger := slog.Default()
	var pgErr *pgconn.PgError
	var tooLarge *http.MaxBytesError

	switch {
	case ctx.Err() != nil && errors.Is(err, context.Canceled):
		// The caller is gone: there is no one to answer and nothing to alert on.
		logger.DebugContext(ctx, "request aborted by the caller", "method", r.Method, "path", r.URL.Path)
	case errors.As(err, &pgErr) && (pgErr.Code == "23505" || pgErr.Code == "23P01"):
		logger.WarnContext(ctx, "request conflicts with existing data",
			"method", r.Method, "path", r.URL.Path, "constraint", pgErr.ConstraintName, "trace_id", TraceID(r))
		WriteProblem(w, r, http.StatusConflict, ConflictDetail)
	case errors.As(err, &tooLarge):
		WriteProblem(w, r, http.StatusRequestEntityTooLarge, TooLargeDetail)
	default:
		logger.ErrorContext(ctx, "request failed",
			"method", r.Method, "path", r.URL.Path, "error", err, "trace_id", TraceID(r))
		WriteProblem(w, r, http.StatusInternalServerError, UnexpectedErrorDetail)
	}
}
```

- [ ] **Step 4: Implement request and trace ids**

Create `apps/server/internal/httpx/trace.go`:

```go
package httpx

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"net/http"

	"go.opentelemetry.io/otel/trace"
)

type requestIDKey struct{}

// RequestID gives every request an id shaped like a W3C trace id (32 hex
// characters), so TraceID has something to report when no span is recording.
func RequestID(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var b [16]byte
		_, _ = rand.Read(b[:])
		ctx := context.WithValue(r.Context(), requestIDKey{}, hex.EncodeToString(b[:]))
		next.ServeHTTP(w, r.WithContext(ctx))
	})
}

// TraceID returns the id that correlates a response with its log lines: the
// OpenTelemetry trace id when a span is active, otherwise the id RequestID
// assigned, otherwise "".
func TraceID(r *http.Request) string {
	if sc := trace.SpanContextFromContext(r.Context()); sc.HasTraceID() {
		return sc.TraceID().String()
	}
	id, _ := r.Context().Value(requestIDKey{}).(string)
	return id
}
```

- [ ] **Step 5: Implement recovery and request logging**

Create `apps/server/internal/httpx/recover.go`:

```go
package httpx

import (
	"log/slog"
	"net/http"
	"runtime/debug"
)

// Recover turns a handler panic into a logged, sanitised 500 problem.
// http.ErrAbortHandler is re-panicked: it is net/http's own signal to abort
// the response silently.
func Recover(logger *slog.Logger) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			defer func() {
				v := recover()
				if v == nil {
					return
				}
				if v == http.ErrAbortHandler {
					panic(v)
				}
				logger.ErrorContext(r.Context(), "panic while serving a request",
					"method", r.Method, "path", r.URL.Path, "panic", v,
					"stack", string(debug.Stack()), "trace_id", TraceID(r))
				WriteProblem(w, r, http.StatusInternalServerError, UnexpectedErrorDetail)
			}()
			next.ServeHTTP(w, r)
		})
	}
}
```

Create `apps/server/internal/httpx/log.go`:

```go
package httpx

import (
	"log/slog"
	"net/http"
	"strings"
	"time"
)

// RequestLog writes one line per request. It logs the path, never the query
// string: invitation, recovery and OIDC callback URLs carry secrets there.
// Health probes log at debug so a 10-second probe does not bury real traffic.
func RequestLog(logger *slog.Logger) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			start := time.Now()
			rec := &statusRecorder{ResponseWriter: w}
			next.ServeHTTP(rec, r)

			status := rec.status
			if status == 0 {
				status = http.StatusOK
			}
			level := slog.LevelInfo
			if strings.HasPrefix(r.URL.Path, "/health/") {
				level = slog.LevelDebug
			}
			logger.LogAttrs(r.Context(), level, "http request",
				slog.String("method", r.Method),
				slog.String("path", r.URL.Path),
				slog.Int("status", status),
				slog.Int64("bytes", rec.bytes),
				slog.Duration("duration", time.Since(start)),
				slog.String("client_ip", ClientIP(r)),
				slog.String("trace_id", TraceID(r)),
			)
		})
	}
}

// statusRecorder captures the status and size of a response. Unwrap keeps
// http.ResponseController (flush, deadlines) working through it.
type statusRecorder struct {
	http.ResponseWriter
	status int
	bytes  int64
}

func (s *statusRecorder) WriteHeader(code int) {
	if s.status == 0 {
		s.status = code
	}
	s.ResponseWriter.WriteHeader(code)
}

func (s *statusRecorder) Write(b []byte) (int, error) {
	if s.status == 0 {
		s.status = http.StatusOK
	}
	n, err := s.ResponseWriter.Write(b)
	s.bytes += int64(n)
	return n, err
}

func (s *statusRecorder) Unwrap() http.ResponseWriter { return s.ResponseWriter }
```

- [ ] **Step 6: Implement forwarded headers, base path and Chain**

Create `apps/server/internal/httpx/forwarded.go`:

```go
package httpx

import (
	"context"
	"net"
	"net/http"
	"net/netip"
	"strings"
)

type forwardedKey struct{}

type forwarded struct {
	clientIP string
	scheme   string
}

// Forwarded resolves the client address and scheme once per request and makes
// them available through ClientIP and Scheme.
//
// With trustedHops = 0 (the default) X-Forwarded-* headers are ignored: the
// peer is the client. With N > 0, the N trusted proxies in front of the
// process each appended one entry to X-Forwarded-For, so the client is the
// Nth entry from the right; everything to its left was written by the client
// and is not trusted. X-Forwarded-Proto's last entry is taken as the scheme.
// X-Forwarded-Host is never honoured: proxies must preserve Host, which host
// filtering checks.
func Forwarded(trustedHops int) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			f := forwarded{clientIP: peerIP(r), scheme: connScheme(r)}
			if trustedHops > 0 {
				if ip, ok := forwardedClient(r.Header.Values("X-Forwarded-For"), trustedHops); ok {
					f.clientIP = ip
				}
				if proto := lastEntry(r.Header.Values("X-Forwarded-Proto")); proto == "http" || proto == "https" {
					f.scheme = proto
				}
			}
			next.ServeHTTP(w, r.WithContext(context.WithValue(r.Context(), forwardedKey{}, f)))
		})
	}
}

// ClientIP is the client's address as resolved by Forwarded, or the peer
// address when the middleware did not run.
func ClientIP(r *http.Request) string {
	if f, ok := r.Context().Value(forwardedKey{}).(forwarded); ok {
		return f.clientIP
	}
	return peerIP(r)
}

// Scheme is "https" or "http" as resolved by Forwarded, or the connection's
// own scheme when the middleware did not run.
func Scheme(r *http.Request) string {
	if f, ok := r.Context().Value(forwardedKey{}).(forwarded); ok {
		return f.scheme
	}
	return connScheme(r)
}

func peerIP(r *http.Request) string {
	if host, _, err := net.SplitHostPort(r.RemoteAddr); err == nil {
		return host
	}
	return r.RemoteAddr
}

func connScheme(r *http.Request) string {
	if r.TLS != nil {
		return "https"
	}
	return "http"
}

func forwardedClient(values []string, hops int) (string, bool) {
	var entries []string
	for _, v := range values {
		for _, part := range strings.Split(v, ",") {
			if p := strings.TrimSpace(part); p != "" {
				entries = append(entries, p)
			}
		}
	}
	if len(entries) < hops {
		return "", false
	}
	candidate := entries[len(entries)-hops]
	if addr, err := netip.ParseAddr(candidate); err == nil {
		return addr.String(), true
	}
	if ap, err := netip.ParseAddrPort(candidate); err == nil {
		return ap.Addr().String(), true
	}
	return "", false
}

func lastEntry(values []string) string {
	if len(values) == 0 {
		return ""
	}
	parts := strings.Split(values[len(values)-1], ",")
	return strings.ToLower(strings.TrimSpace(parts[len(parts)-1]))
}
```

Create `apps/server/internal/httpx/basepath.go`:

```go
package httpx

import (
	"net/http"
	"net/url"
	"strings"
)

// StripBasePath mounts the application under base (e.g. "/vantigo"): a
// request under the prefix continues with the prefix removed, and a request
// outside it passes through untouched — so container probes of /health/ready
// keep working, as with ASP.NET Core's UsePathBase. base "" is a no-op.
func StripBasePath(base string) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		if base == "" {
			return next
		}
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			p := r.URL.Path
			if p != base && !strings.HasPrefix(p, base+"/") {
				next.ServeHTTP(w, r)
				return
			}
			r2 := new(http.Request)
			*r2 = *r
			r2.URL = new(url.URL)
			*r2.URL = *r.URL
			r2.URL.Path = strings.TrimPrefix(p, base)
			if r2.URL.Path == "" {
				r2.URL.Path = "/"
			}
			r2.URL.RawPath = ""
			next.ServeHTTP(w, r2)
		})
	}
}
```

Create `apps/server/internal/httpx/chain.go`:

```go
package httpx

import "net/http"

// Chain wraps h in middleware so that the first one listed is the outermost:
// Chain(h, a, b) serves a(b(h)).
func Chain(h http.Handler, middleware ...func(http.Handler) http.Handler) http.Handler {
	for i := len(middleware) - 1; i >= 0; i-- {
		h = middleware[i](h)
	}
	return h
}
```

- [ ] **Step 7: Add the dependency and run the tests**

Run: `cd apps/server && go get go.opentelemetry.io/otel/trace@v1.46.0 && go mod tidy && go test ./internal/httpx/ -v -count=1 && golangci-lint run`
Expected: every test PASS; `0 issues.`

- [ ] **Step 8: Commit**

```bash
git add apps/server/go.mod apps/server/go.sum apps/server/internal/httpx
git commit -m "feat(server): add the shared HTTP plumbing

RFC 7807 problems with sanitised details, request/trace ids, panic
recovery, a query-string-free request log, forwarded-header trust by hop
count, and base-path mounting."
```

---

### Task 4: Security headers and host filtering

**Files:**
- Create: `apps/server/internal/security/headers.go`, `apps/server/internal/security/hostfilter.go`
- Test: `apps/server/internal/security/headers_test.go`, `apps/server/internal/security/hostfilter_test.go`

**Interfaces:**
- Consumes: `httpx.Scheme(r)`, `httpx.WriteProblem` (Task 3).
- Produces:
  - consts `ReferrerPolicy`, `PermissionsPolicy`
  - `func ContentSecurityPolicy(inlineScriptHash string) string`
  - `type HeaderOptions struct { InlineScriptHash string; ReportOnly bool; HSTS bool }`
  - `func Headers(o HeaderOptions) func(http.Handler) http.Handler`
  - `func AllowedHosts(appHostname string) []string`, `func HostFilter(allowed []string) func(http.Handler) http.Handler`

Header values are carried over byte-for-byte from `apps/host/backend/Vantigo.Host/Security/SecurityHeaders.cs`. HSTS matches ASP.NET Core's `UseHsts` defaults: 30 days, no `includeSubDomains`, only on https, never for loopback hosts.

- [ ] **Step 1: Write the failing tests**

Create `apps/server/internal/security/headers_test.go`:

```go
package security

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/vantigo-io/vantigo/server/internal/httpx"
)

const testHash = "'sha256-abc123='"

func serveWithHeaders(o HeaderOptions, req *http.Request) http.Header {
	rec := httptest.NewRecorder()
	httpx.Chain(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {}),
		httpx.Forwarded(1), Headers(o)).ServeHTTP(rec, req)
	return rec.Header()
}

func TestContentSecurityPolicy_MatchesTheDotNetHostPolicy(t *testing.T) {
	want := "default-src 'self'; base-uri 'self'; object-src 'none'; frame-ancestors 'none'; " +
		"form-action 'self'; script-src 'self' 'sha256-abc123='; style-src 'self' 'unsafe-inline'; " +
		"img-src 'self' data: blob: https:; font-src 'self' data:; connect-src 'self'; " +
		"frame-src 'self'; worker-src 'self'; manifest-src 'self'"
	if got := ContentSecurityPolicy(testHash); got != want {
		t.Errorf("policy =\n%s\nwant\n%s", got, want)
	}
}

func TestHeaders_SetsTheFullSetOnEveryResponse(t *testing.T) {
	h := serveWithHeaders(HeaderOptions{InlineScriptHash: testHash}, httptest.NewRequest(http.MethodGet, "http://vantigo.example.com/api/x", nil))

	want := map[string]string{
		"Content-Security-Policy": ContentSecurityPolicy(testHash),
		"X-Content-Type-Options":  "nosniff",
		"X-Frame-Options":         "DENY",
		"Referrer-Policy":         "no-referrer",
		"Permissions-Policy":      PermissionsPolicy,
	}
	for name, value := range want {
		if got := h.Get(name); got != value {
			t.Errorf("%s = %q, want %q", name, got, value)
		}
	}
	if h.Get("Content-Security-Policy-Report-Only") != "" {
		t.Error("report-only header set while enforcing")
	}
}

func TestHeaders_ReportOnlyMovesThePolicyToTheReportOnlyHeader(t *testing.T) {
	h := serveWithHeaders(HeaderOptions{InlineScriptHash: testHash, ReportOnly: true}, httptest.NewRequest(http.MethodGet, "/", nil))

	if h.Get("Content-Security-Policy") != "" {
		t.Error("enforcing header set in report-only mode")
	}
	if h.Get("Content-Security-Policy-Report-Only") != ContentSecurityPolicy(testHash) {
		t.Errorf("report-only header = %q", h.Get("Content-Security-Policy-Report-Only"))
	}
}

func TestHeaders_HSTS(t *testing.T) {
	forwardedHTTPS := func(target string) *http.Request {
		r := httptest.NewRequest(http.MethodGet, target, nil)
		r.Header.Set("X-Forwarded-Proto", "https")
		r.Header.Set("X-Forwarded-For", "203.0.113.7")
		return r
	}
	tests := []struct {
		name string
		hsts bool
		req  *http.Request
		want string
	}{
		{"https behind a trusted proxy", true, forwardedHTTPS("http://vantigo.example.com/"), "max-age=2592000"},
		{"https on the connection", true, httptest.NewRequest(http.MethodGet, "https://vantigo.example.com/", nil), "max-age=2592000"},
		{"plain http", true, httptest.NewRequest(http.MethodGet, "http://vantigo.example.com/", nil), ""},
		{"loopback host", true, forwardedHTTPS("http://localhost:8080/"), ""},
		{"disabled (development)", false, forwardedHTTPS("http://vantigo.example.com/"), ""},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			h := serveWithHeaders(HeaderOptions{InlineScriptHash: testHash, HSTS: tc.hsts}, tc.req)
			if got := h.Get("Strict-Transport-Security"); got != tc.want {
				t.Errorf("Strict-Transport-Security = %q, want %q", got, tc.want)
			}
		})
	}
}
```

Create `apps/server/internal/security/hostfilter_test.go`:

```go
package security

import (
	"net/http"
	"net/http/httptest"
	"slices"
	"testing"
)

func TestAllowedHosts_AddsLoopbackOnce(t *testing.T) {
	if got := AllowedHosts("vantigo.example.com"); !slices.Equal(got, []string{"vantigo.example.com", "localhost", "127.0.0.1", "::1"}) {
		t.Errorf("AllowedHosts = %v", got)
	}
	if got := AllowedHosts("localhost"); !slices.Equal(got, []string{"localhost", "127.0.0.1", "::1"}) {
		t.Errorf("AllowedHosts(localhost) = %v", got)
	}
}

func TestHostFilter(t *testing.T) {
	filter := HostFilter(AllowedHosts("vantigo.example.com"))
	tests := []struct {
		host string
		want int
	}{
		{"vantigo.example.com", http.StatusOK},
		{"VANTIGO.example.com:443", http.StatusOK},
		{"localhost:8080", http.StatusOK},
		{"127.0.0.1:8080", http.StatusOK},
		{"[::1]:8080", http.StatusOK},
		{"evil.example", http.StatusBadRequest},
		{"vantigo.example.com.evil.example", http.StatusBadRequest},
		{"", http.StatusBadRequest},
	}
	for _, tc := range tests {
		t.Run(tc.host, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodGet, "/", nil)
			req.Host = tc.host
			rec := httptest.NewRecorder()
			filter(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				w.WriteHeader(http.StatusOK)
			})).ServeHTTP(rec, req)

			if rec.Code != tc.want {
				t.Errorf("Host %q: status %d, want %d", tc.host, rec.Code, tc.want)
			}
			if tc.want == http.StatusBadRequest && rec.Header().Get("Content-Type") != "application/problem+json" {
				t.Errorf("rejection is not a problem document: %q", rec.Header().Get("Content-Type"))
			}
		})
	}
}
```

- [ ] **Step 2: Run the tests to verify they fail**

Run: `cd apps/server && go test ./internal/security/`
Expected: FAIL to compile with `undefined: HeaderOptions`, `undefined: AllowedHosts`, and similar.

- [ ] **Step 3: Implement the headers**

Create `apps/server/internal/security/headers.go`:

```go
// Package security holds the browser-facing hardening applied to every
// response: the security header set with the content security policy, and
// host filtering.
package security

import (
	"net/http"
	"strings"

	"github.com/vantigo-io/vantigo/server/internal/httpx"
)

// ReferrerPolicy: an internal back office; no outbound link needs to disclose
// the page a user came from.
const ReferrerPolicy = "no-referrer"

// PermissionsPolicy denies powerful features the application never uses.
const PermissionsPolicy = "accelerometer=(), autoplay=(), browsing-topics=(), camera=(), display-capture=(), " +
	"encrypted-media=(), fullscreen=(self), geolocation=(), gyroscope=(), " +
	"idle-detection=(), local-fonts=(), magnetometer=(), microphone=(), midi=(), " +
	"payment=(), picture-in-picture=(), screen-wake-lock=(), serial=(), usb=(), " +
	"xr-spatial-tracking=()"

// hstsValue is ASP.NET Core's UseHsts default: 30 days, no includeSubDomains.
const hstsValue = "max-age=2592000"

// ContentSecurityPolicy builds the policy that allows inlineScriptHash — the
// runtime-configuration script web.Index injects — as the only inline script.
// style-src keeps 'unsafe-inline' because Mantine renders its CSS variables
// and component styles as inline <style> elements and React style props as
// style attributes. img-src allows https: for the configurable logo URL and
// remote images in the sandboxed email preview.
func ContentSecurityPolicy(inlineScriptHash string) string {
	return strings.Join([]string{
		"default-src 'self'",
		"base-uri 'self'",
		"object-src 'none'",
		"frame-ancestors 'none'",
		"form-action 'self'",
		"script-src 'self' " + inlineScriptHash,
		"style-src 'self' 'unsafe-inline'",
		"img-src 'self' data: blob: https:",
		"font-src 'self' data:",
		"connect-src 'self'",
		"frame-src 'self'",
		"worker-src 'self'",
		"manifest-src 'self'",
	}, "; ")
}

// HeaderOptions configures Headers.
type HeaderOptions struct {
	// InlineScriptHash is the CSP source expression for the index document's
	// runtime-configuration script ('sha256-…').
	InlineScriptHash string
	// ReportOnly sends the policy as Content-Security-Policy-Report-Only.
	ReportOnly bool
	// HSTS enables Strict-Transport-Security on https requests to non-loopback
	// hosts. Off in development.
	HSTS bool
}

// Headers sets the security header set on every response, API responses
// included. It must run after httpx.Forwarded so the HSTS decision sees the
// scheme the client used.
func Headers(o HeaderOptions) func(http.Handler) http.Handler {
	policyHeader := "Content-Security-Policy"
	if o.ReportOnly {
		policyHeader = "Content-Security-Policy-Report-Only"
	}
	policy := ContentSecurityPolicy(o.InlineScriptHash)

	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			h := w.Header()
			h.Set(policyHeader, policy)
			h.Set("X-Content-Type-Options", "nosniff")
			h.Set("X-Frame-Options", "DENY")
			h.Set("Referrer-Policy", ReferrerPolicy)
			h.Set("Permissions-Policy", PermissionsPolicy)
			if o.HSTS && httpx.Scheme(r) == "https" && !isLoopback(requestHostname(r.Host)) {
				h.Set("Strict-Transport-Security", hstsValue)
			}
			next.ServeHTTP(w, r)
		})
	}
}
```

- [ ] **Step 4: Implement host filtering**

Create `apps/server/internal/security/hostfilter.go`:

```go
package security

import (
	"net"
	"net/http"
	"slices"
	"strings"

	"github.com/vantigo-io/vantigo/server/internal/httpx"
)

// loopbackHosts stay allowed whatever APP_URL is: the container HEALTHCHECK
// (`vantigo healthcheck`) probes http://127.0.0.1:<port>/health/ready, and
// host filtering runs before routing, so it cannot exempt that path by name.
var loopbackHosts = []string{"localhost", "127.0.0.1", "::1"}

// AllowedHosts is APP_URL's host plus loopback.
func AllowedHosts(appHostname string) []string {
	hosts := []string{strings.ToLower(appHostname)}
	for _, h := range loopbackHosts {
		if !slices.Contains(hosts, h) {
			hosts = append(hosts, h)
		}
	}
	return hosts
}

// HostFilter rejects any request whose Host header is not in allowed (the port
// is ignored), so a request carrying someone else's host name never reaches
// the application.
func HostFilter(allowed []string) func(http.Handler) http.Handler {
	set := make(map[string]bool, len(allowed))
	for _, h := range allowed {
		set[strings.ToLower(h)] = true
	}
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if !set[requestHostname(r.Host)] {
				httpx.WriteProblem(w, r, http.StatusBadRequest, "The request host is not allowed.")
				return
			}
			next.ServeHTTP(w, r)
		})
	}
}

// requestHostname is the Host header without port or IPv6 brackets, lower-case.
func requestHostname(host string) string {
	if h, _, err := net.SplitHostPort(host); err == nil {
		return strings.ToLower(h)
	}
	return strings.ToLower(strings.TrimSuffix(strings.TrimPrefix(host, "["), "]"))
}

func isLoopback(hostname string) bool { return slices.Contains(loopbackHosts, hostname) }
```

- [ ] **Step 5: Run the tests to verify they pass**

Run: `cd apps/server && go test ./internal/security/ -v -count=1 && golangci-lint run`
Expected: every test PASS; `0 issues.`

- [ ] **Step 6: Commit**

```bash
git add apps/server/internal/security
git commit -m "feat(server): add security headers and host filtering

The .NET host's CSP, Permissions-Policy and companion headers on every
response, HSTS on https for non-loopback hosts, and a host allowlist of
APP_URL's host plus loopback."
```

---

### Task 5: Embedded SPA, index templating and the SPA handler

**Files:**
- Create: `apps/server/internal/web/web.go`, `apps/server/internal/web/index.go`, `apps/server/internal/web/handler.go`
- Create: `apps/server/internal/web/dist/index.html` (committed placeholder)
- Test: `apps/server/internal/web/index_test.go`, `apps/server/internal/web/handler_test.go`

**Interfaces:**
- Consumes: `config.Branding` (Task 2), `httpx.WriteProblem` (Task 3).
- Produces:
  - `func Assets() fs.FS` — the embedded build rooted at `dist`
  - `const DefaultTitle = "Vantigo"`
  - `type Index struct { HTML []byte; InlineScriptHash string }`
  - `func NewIndex(assets fs.FS, basePath string, b config.Branding) (*Index, error)` — `basePath` is `config.Config.BasePath` (`""` or `"/prefix"`)
  - `func Handler(assets fs.FS, index *Index) http.Handler`

Ported from `apps/host/backend/Vantigo.Host/Spa/SpaIndexDocument.cs` and its tests. The Vite build uses `base: "/"`, so the .NET "different build-time base path" branch isn't carried over. Go's JSON encoder escapes `<`, `>` and `&` as `\u003c`, `\u003e` and `\u0026` (lower-case hex), so the config script can't be terminated early. Unlike .NET, it leaves `+` and non-ASCII characters alone, which is valid in a UTF-8 document.

- [ ] **Step 1: Create the placeholder**

Create `apps/server/internal/web/dist/index.html`:

```html
<!doctype html>
<html lang="en">
  <head>
    <meta charset="UTF-8" />
    <title>Vantigo</title>
  </head>
  <body>
    <p>This binary was built without the frontend. Build it with scripts/build-artifacts.sh, or run the Vite dev server.</p>
  </body>
</html>
```

The phrase "built without the frontend" is what `scripts/smoke-image.sh` (Task 12) greps for, so that an image embedding the placeholder fails the smoke test.

- [ ] **Step 2: Write the failing tests**

Create `apps/server/internal/web/index_test.go`:

```go
package web

import (
	"crypto/sha256"
	"encoding/base64"
	"io/fs"
	"strings"
	"testing"
	"testing/fstest"

	"github.com/vantigo-io/vantigo/server/internal/config"
)

const builtIndexHTML = `<!doctype html>
<html lang="en">
  <head>
    <meta charset="UTF-8" />
    <link rel="icon" type="image/png" href="/favicon.png" />
    <title>Vantigo</title>
    <script type="module" crossorigin src="/assets/index-abc123.js"></script>
    <link rel="stylesheet" crossorigin href="/assets/index-def456.css">
  </head>
  <body>
    <div id="root"></div>
  </body>
</html>`

func renderIndex(t *testing.T, basePath string, b config.Branding, html string) (*Index, string) {
	t.Helper()
	idx, err := NewIndex(fstest.MapFS{"index.html": {Data: []byte(html)}}, basePath, b)
	if err != nil {
		t.Fatalf("NewIndex: %v", err)
	}
	return idx, string(idx.HTML)
}

func assertContains(t *testing.T, html string, wants ...string) {
	t.Helper()
	for _, want := range wants {
		if !strings.Contains(html, want) {
			t.Errorf("document is missing %q:\n%s", want, html)
		}
	}
}

func assertNotContains(t *testing.T, html string, unwanted ...string) {
	t.Helper()
	for _, s := range unwanted {
		if strings.Contains(html, s) {
			t.Errorf("document contains %q:\n%s", s, html)
		}
	}
}

func TestNewIndex_AtTheRootKeepsAssetURLs(t *testing.T) {
	_, html := renderIndex(t, "", config.Branding{}, builtIndexHTML)
	assertContains(t, html, `src="/assets/index-abc123.js"`, `href="/favicon.png"`, `"basePath":"/"`)
}

func TestNewIndex_UnderABasePathRewritesEveryAssetURL(t *testing.T) {
	_, html := renderIndex(t, "/crm", config.Branding{}, builtIndexHTML)
	assertContains(t, html,
		`src="/crm/assets/index-abc123.js"`,
		`href="/crm/assets/index-def456.css"`,
		`href="/crm/favicon.png"`,
		`"basePath":"/crm/"`)
	assertNotContains(t, html, `src="/assets/`, `href="/favicon.png"`)
}

func TestNewIndex_InjectsTheConfigBeforeAnyModuleScript(t *testing.T) {
	_, html := renderIndex(t, "", config.Branding{}, builtIndexHTML)
	head := strings.Index(html, "<head>")
	script := strings.Index(html, "window.__VANTIGO_APP__")
	module := strings.Index(html, `type="module"`)
	if head < 0 || script < head || script > module {
		t.Errorf("config script at %d is not between <head> at %d and the module script at %d", script, head, module)
	}
}

func TestNewIndex_DefaultBrandingInjectsNulls(t *testing.T) {
	_, html := renderIndex(t, "", config.Branding{}, builtIndexHTML)
	assertContains(t, html,
		`window.__VANTIGO_APP__={"basePath":"/","title":"Vantigo","logoUrl":null,"support":{"email":null,"phone":null,"url":null}};`)
}

func TestNewIndex_FullBrandingInjectsEveryValue(t *testing.T) {
	_, html := renderIndex(t, "", config.Branding{
		Title:        "Acme ERP",
		LogoURL:      "https://cdn.acme.test/logo.svg",
		SupportEmail: "help@acme.test",
		SupportPhone: "+47 123 45 678",
		SupportURL:   "https://support.acme.test",
	}, builtIndexHTML)
	assertContains(t, html,
		`"title":"Acme ERP"`,
		`"logoUrl":"https://cdn.acme.test/logo.svg"`,
		`"email":"help@acme.test"`,
		`"phone":"+47 123 45 678"`,
		`"url":"https://support.acme.test"`)
}

func TestNewIndex_ReplacesTheDocumentTitle(t *testing.T) {
	_, html := renderIndex(t, "", config.Branding{Title: "Acme ERP"}, builtIndexHTML)
	assertContains(t, html, "<title>Acme ERP</title>")
	assertNotContains(t, html, "<title>Vantigo</title>")
}

func TestNewIndex_WithoutATitleElementStillInjectsTheConfig(t *testing.T) {
	_, html := renderIndex(t, "/crm", config.Branding{}, `<html><head><script src="/assets/a.js"></script></head></html>`)
	assertContains(t, html, "window.__VANTIGO_APP__", `src="/crm/assets/a.js"`)
	assertNotContains(t, html, "<title>")
}

func TestNewIndex_HostileTitleCannotBreakOutOfTheScriptOrTitle(t *testing.T) {
	_, html := renderIndex(t, "", config.Branding{Title: `</script><script>alert(1)</script>`}, builtIndexHTML)
	assertNotContains(t, html, "<script>alert(1)</script>")
	assertContains(t, html,
		`\u003c/script\u003e`,
		"<title>&lt;/script&gt;&lt;script&gt;alert(1)&lt;/script&gt;</title>")
}

func TestNewIndex_QuotesAndUnicodeProduceValidJSONAndHTML(t *testing.T) {
	_, html := renderIndex(t, "", config.Branding{Title: `Møller "Bil" & Co`}, builtIndexHTML)
	assertContains(t, html,
		`"title":"Møller \"Bil\" \u0026 Co"`,
		"<title>Møller &#34;Bil&#34; &amp; Co</title>")
}

func TestNewIndex_HostileLogoURLCannotBreakOutOfTheScript(t *testing.T) {
	// config rejects this value; the renderer must be safe on its own anyway.
	_, html := renderIndex(t, "", config.Branding{LogoURL: `x"};</script><script>alert(1)//`}, builtIndexHTML)
	assertNotContains(t, html, "</script><script>alert(1)")
}

func TestNewIndex_ScriptHashCoversExactlyTheInjectedScript(t *testing.T) {
	idx, html := renderIndex(t, "/crm", config.Branding{Title: "Acme"}, builtIndexHTML)

	start := strings.Index(html, "<head><script>") + len("<head><script>")
	end := start + strings.Index(html[start:], "</script>")
	sum := sha256.Sum256([]byte(html[start:end]))
	want := "'sha256-" + base64.StdEncoding.EncodeToString(sum[:]) + "'"

	if idx.InlineScriptHash != want {
		t.Errorf("InlineScriptHash = %s, want %s (the hash of the script actually injected)", idx.InlineScriptHash, want)
	}
}

func TestNewIndex_LeavesUnrelatedContentUntouched(t *testing.T) {
	_, html := renderIndex(t, "/crm", config.Branding{}, builtIndexHTML)
	assertContains(t, html, `<div id="root"></div>`, `<meta charset="UTF-8" />`)
}

func TestNewIndex_MissingIndexIsAnError(t *testing.T) {
	if _, err := NewIndex(fstest.MapFS{}, "", config.Branding{}); err == nil {
		t.Fatal("NewIndex succeeded without an index.html")
	}
}

func TestAssets_ContainsAnIndexDocument(t *testing.T) {
	if _, err := fs.ReadFile(Assets(), "index.html"); err != nil {
		t.Fatalf("embedded build has no index.html: %v", err)
	}
}
```

Create `apps/server/internal/web/handler_test.go`:

```go
package web

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"testing/fstest"

	"github.com/vantigo-io/vantigo/server/internal/config"
)

func testHandler(t *testing.T) (http.Handler, *Index) {
	t.Helper()
	assets := fstest.MapFS{
		"index.html":             {Data: []byte(builtIndexHTML)},
		"favicon.png":            {Data: []byte("png")},
		"assets/index-abc123.js": {Data: []byte("console.log(1)")},
	}
	idx, err := NewIndex(assets, "", config.Branding{})
	if err != nil {
		t.Fatalf("NewIndex: %v", err)
	}
	return Handler(assets, idx), idx
}

func get(h http.Handler, method, path string) *httptest.ResponseRecorder {
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(method, path, nil))
	return rec
}

func TestHandler_ServesTheTemplatedIndexForClientRoutes(t *testing.T) {
	h, idx := testHandler(t)
	for _, path := range []string{"/", "/customers/123", "/settings/roles", "/index.html"} {
		rec := get(h, http.MethodGet, path)
		if rec.Code != http.StatusOK || rec.Body.String() != string(idx.HTML) {
			t.Errorf("%s: status %d, templated index served = %v", path, rec.Code, rec.Body.String() == string(idx.HTML))
		}
		if rec.Header().Get("Content-Type") != "text/html; charset=utf-8" || rec.Header().Get("Cache-Control") != "no-cache" {
			t.Errorf("%s: Content-Type %q, Cache-Control %q", path, rec.Header().Get("Content-Type"), rec.Header().Get("Cache-Control"))
		}
	}
}

func TestHandler_ServesHashedAssetsAsImmutable(t *testing.T) {
	h, _ := testHandler(t)
	rec := get(h, http.MethodGet, "/assets/index-abc123.js")

	if rec.Code != http.StatusOK || rec.Body.String() != "console.log(1)" {
		t.Fatalf("status %d body %q", rec.Code, rec.Body.String())
	}
	if rec.Header().Get("Cache-Control") != "public, max-age=31536000, immutable" {
		t.Errorf("Cache-Control = %q", rec.Header().Get("Cache-Control"))
	}
	if !strings.HasPrefix(rec.Header().Get("Content-Type"), "text/javascript") {
		t.Errorf("Content-Type = %q", rec.Header().Get("Content-Type"))
	}
}

func TestHandler_ServesOtherFilesWithoutImmutableCaching(t *testing.T) {
	h, _ := testHandler(t)
	rec := get(h, http.MethodGet, "/favicon.png")
	if rec.Code != http.StatusOK || rec.Body.String() != "png" || rec.Header().Get("Cache-Control") != "" {
		t.Errorf("status %d body %q Cache-Control %q", rec.Code, rec.Body.String(), rec.Header().Get("Cache-Control"))
	}
}

func TestHandler_UnknownHashedAssetIs404NotTheIndex(t *testing.T) {
	h, _ := testHandler(t)
	if rec := get(h, http.MethodGet, "/assets/index-stale.js"); rec.Code != http.StatusNotFound {
		t.Errorf("status %d, want 404", rec.Code)
	}
}

func TestHandler_HEADHasNoBody(t *testing.T) {
	h, _ := testHandler(t)
	rec := get(h, http.MethodHead, "/customers")
	if rec.Code != http.StatusOK || rec.Body.Len() != 0 {
		t.Errorf("status %d body length %d", rec.Code, rec.Body.Len())
	}
}

func TestHandler_RejectsOtherMethods(t *testing.T) {
	h, _ := testHandler(t)
	rec := get(h, http.MethodPost, "/customers")
	if rec.Code != http.StatusMethodNotAllowed || rec.Header().Get("Allow") != "GET, HEAD" {
		t.Errorf("status %d Allow %q", rec.Code, rec.Header().Get("Allow"))
	}
}
```

- [ ] **Step 3: Run the tests to verify they fail**

Run: `cd apps/server && go test ./internal/web/`
Expected: FAIL to compile with `undefined: NewIndex`, `undefined: Handler`, `undefined: Assets`.

- [ ] **Step 4: Implement the package**

Create `apps/server/internal/web/web.go`:

```go
// Package web serves the SPA: the Vite build compiled into the binary, its
// index document templated once at startup, and the fallback that hands every
// client-side route to that document.
package web

import (
	"embed"
	"io/fs"
)

// distFS is the frontend build. dist/index.html is a committed placeholder so
// the package builds and tests without a frontend build.
// scripts/spa-embed-overlay.sh replaces the directory with the Vite output
// before release builds; scripts/restore-embed-overlay.sh puts the placeholder
// back.
//
//go:embed all:dist
var distFS embed.FS

// Assets is the embedded frontend build rooted at its dist directory.
func Assets() fs.FS {
	sub, err := fs.Sub(distFS, "dist")
	if err != nil {
		panic("web: the embedded dist directory is missing: " + err.Error())
	}
	return sub
}
```

Create `apps/server/internal/web/index.go`:

```go
package web

import (
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"html"
	"io/fs"
	"regexp"
	"strings"

	"github.com/vantigo-io/vantigo/server/internal/config"
)

// DefaultTitle is the document title when APP_TITLE is not set.
const DefaultTitle = "Vantigo"

// Index is the SPA entry document, templated once at startup so one image
// serves any base path and branding without rebuilding the frontend.
type Index struct {
	HTML []byte
	// InlineScriptHash is the CSP source expression ('sha256-…') for the one
	// inline script the document carries. It is derived from the same string
	// that is injected, so the policy cannot drift from the script.
	InlineScriptHash string
}

// NewIndex reads index.html from assets and templates it: asset URLs are
// rewritten under basePath, window.__VANTIGO_APP__ is injected as the first
// element of <head>, and <title> is replaced.
func NewIndex(assets fs.FS, basePath string, b config.Branding) (*Index, error) {
	raw, err := fs.ReadFile(assets, "index.html")
	if err != nil {
		return nil, fmt.Errorf("web: read index.html: %w", err)
	}
	script := runtimeConfigScript(basePath, b)
	sum := sha256.Sum256([]byte(script))
	return &Index{
		HTML:             []byte(render(string(raw), basePath, title(b), script)),
		InlineScriptHash: "'sha256-" + base64.StdEncoding.EncodeToString(sum[:]) + "'",
	}, nil
}

var titleElement = regexp.MustCompile(`(?s)<title>.*?</title>`)

func render(doc, basePath, title, script string) string {
	if basePath != "" {
		doc = strings.ReplaceAll(doc, `src="/`, `src="`+basePath+`/`)
		doc = strings.ReplaceAll(doc, `href="/`, `href="`+basePath+`/`)
	}
	// The config must exist before any module script in <head> runs.
	doc = strings.Replace(doc, "<head>", "<head><script>"+script+"</script>", 1)
	if loc := titleElement.FindStringIndex(doc); loc != nil {
		doc = doc[:loc[0]] + "<title>" + html.EscapeString(title) + "</title>" + doc[loc[1]:]
	}
	return doc
}

// runtimeConfig is the shape the frontends read from window.__VANTIGO_APP__
// (see apps/*/frontend/src/lib/app-config.ts). Unset values are null.
type runtimeConfig struct {
	BasePath string         `json:"basePath"`
	Title    string         `json:"title"`
	LogoURL  *string        `json:"logoUrl"`
	Support  runtimeSupport `json:"support"`
}

type runtimeSupport struct {
	Email *string `json:"email"`
	Phone *string `json:"phone"`
	URL   *string `json:"url"`
}

// runtimeConfigScript is the exact text of the injected script. Both the
// document and the CSP hash come from this one function. encoding/json escapes
// <, > and &, so no value can close the <script> element.
func runtimeConfigScript(basePath string, b config.Branding) string {
	data, err := json.Marshal(runtimeConfig{
		BasePath: basePath + "/",
		Title:    title(b),
		LogoURL:  optional(b.LogoURL),
		Support: runtimeSupport{
			Email: optional(b.SupportEmail),
			Phone: optional(b.SupportPhone),
			URL:   optional(b.SupportURL),
		},
	})
	if err != nil {
		panic("web: marshal runtime config: " + err.Error()) // impossible for this type
	}
	return "window.__VANTIGO_APP__=" + string(data) + ";"
}

func title(b config.Branding) string {
	if t := strings.TrimSpace(b.Title); t != "" {
		return t
	}
	return DefaultTitle
}

func optional(s string) *string {
	if s == "" {
		return nil
	}
	return &s
}
```

Create `apps/server/internal/web/handler.go`:

```go
package web

import (
	"io/fs"
	"net/http"
	"path"
	"strings"

	"github.com/vantigo-io/vantigo/server/internal/httpx"
)

// Handler serves real files from assets and the templated index for every
// other GET/HEAD, so the client-side router (TanStack Router) resolves deep
// links. /index.html is answered with the templated document, never the raw
// file. Vite's content-hashed bundles under assets/ are cached as immutable;
// a missing one is a 404, not HTML where the browser expects JavaScript.
func Handler(assets fs.FS, index *Index) http.Handler {
	files := http.FileServerFS(assets)
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet && r.Method != http.MethodHead {
			w.Header().Set("Allow", "GET, HEAD")
			httpx.WriteProblem(w, r, http.StatusMethodNotAllowed, "")
			return
		}

		name := strings.TrimPrefix(path.Clean("/"+r.URL.Path), "/")
		if name != "" && name != "index.html" && isFile(assets, name) {
			if strings.HasPrefix(name, "assets/") {
				w.Header().Set("Cache-Control", "public, max-age=31536000, immutable")
			}
			files.ServeHTTP(w, r)
			return
		}
		if strings.HasPrefix(name, "assets/") {
			http.NotFound(w, r)
			return
		}

		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		w.Header().Set("Cache-Control", "no-cache")
		w.WriteHeader(http.StatusOK)
		if r.Method == http.MethodGet {
			_, _ = w.Write(index.HTML)
		}
	})
}

func isFile(fsys fs.FS, name string) bool {
	info, err := fs.Stat(fsys, name)
	return err == nil && !info.IsDir()
}
```

- [ ] **Step 5: Run the tests to verify they pass**

Run: `cd apps/server && go test ./internal/web/ -v -count=1 && golangci-lint run`
Expected: every test PASS; `0 issues.`

- [ ] **Step 6: Commit**

```bash
git add apps/server/internal/web
git commit -m "feat(server): serve the embedded SPA with a templated index

Port of SpaIndexDocument: base-path rewriting, the window.__VANTIGO_APP__
runtime config injected first in <head>, title replacement, and a CSP hash
derived from the injected script itself. The handler serves hashed assets
as immutable and every client route with the templated index."
```

---

### Task 6: Database access, migrations and per-test databases

**Files:**
- Create: `docker-compose.test.yml`
- Create: `apps/server/internal/db/db.go`, `apps/server/internal/db/migrate.go`, `apps/server/internal/db/export_test.go`
- Create: `apps/server/internal/db/migrations/00001_platform_init.sql`
- Create: `apps/server/internal/testdb/testdb.go`
- Test: `apps/server/internal/db/db_test.go`, `apps/server/internal/testdb/testdb_test.go`

**Interfaces:**
- Consumes: nothing from earlier tasks.
- Produces:
  - `func db.Open(ctx context.Context, databaseURL string) (*pgxpool.Pool, error)` — pings, retrying for about 30 s while PostgreSQL comes up
  - `const db.MigrationLockKey int64 = 0x56414E5449474F31` (same key as the .NET `MigrationLock`)
  - `func db.ApplyMigrations(ctx context.Context, databaseURL string) error`
  - `func testdb.URL(t testing.TB) string` — a fresh, empty database, dropped at cleanup
  - `func testdb.Migrated(t testing.TB) (*pgxpool.Pool, string)` — fresh database, migrations applied, pool closed at cleanup
  - table `platform.rate_limit(key text PK, window_start timestamptz, hits integer)`

Migration files are named `NNNNN_<owner>_<name>.sql` (spec §3.4). `platform` owns process-wide infrastructure; each module's baseline migration will create its own schema.

- [ ] **Step 1: Start a test database server**

Create `docker-compose.test.yml`:

```yaml
# The PostgreSQL server the Go tests run against:
#
#   docker compose -f docker-compose.test.yml up -d --wait
#   cd apps/server && go test ./...
#
# Every test creates and drops its own database on this server
# (apps/server/internal/testdb), so nothing needs resetting between runs.
# No volume and durability switched off: test data is meant to be thrown away.
name: vantigo-test

services:
  postgres:
    image: postgres:18-alpine
    environment:
      POSTGRES_USER: vantigo
      POSTGRES_PASSWORD: vantigo
      POSTGRES_DB: vantigo_test
    command: ["postgres", "-c", "fsync=off", "-c", "synchronous_commit=off", "-c", "full_page_writes=off"]
    ports:
      # Loopback only, on a port that will not collide with a local PostgreSQL.
      - "127.0.0.1:55432:5432"
    tmpfs:
      - /var/lib/postgresql
    healthcheck:
      test: ["CMD-SHELL", "pg_isready -U vantigo -d vantigo_test"]
      interval: 2s
      timeout: 3s
      retries: 30
```

Run: `docker compose -f docker-compose.test.yml up -d --wait`
Expected: `Container vantigo-test-postgres-1  Healthy`.

- [ ] **Step 2: Write the failing testdb test**

Create `apps/server/internal/testdb/testdb_test.go`:

```go
package testdb

import (
	"context"
	"net/url"
	"os"
	"strings"
	"testing"

	"github.com/jackc/pgx/v5"
)

func databaseExists(t *testing.T, name string) bool {
	t.Helper()
	ctx := context.Background()
	conn, err := pgx.Connect(ctx, baseURL())
	if err != nil {
		t.Fatalf("connect: %v", err)
	}
	defer func() { _ = conn.Close(ctx) }()
	var exists bool
	if err := conn.QueryRow(ctx, "SELECT EXISTS (SELECT 1 FROM pg_database WHERE datname = $1)", name).Scan(&exists); err != nil {
		t.Fatalf("query pg_database: %v", err)
	}
	return exists
}

func TestURL_GivesEachTestItsOwnDatabaseAndDropsIt(t *testing.T) {
	var first, second string
	t.Run("create", func(t *testing.T) {
		first, second = URL(t), URL(t)
		if first == second {
			t.Fatal("two calls returned the same database")
		}
		for _, raw := range []string{first, second} {
			conn, err := pgx.Connect(context.Background(), raw)
			if err != nil {
				t.Fatalf("connect to %s: %v", raw, err)
			}
			_ = conn.Close(context.Background())
		}
	})

	for _, raw := range []string{first, second} {
		u, err := url.Parse(raw)
		if err != nil {
			t.Fatal(err)
		}
		if name := strings.TrimPrefix(u.Path, "/"); databaseExists(t, name) {
			t.Errorf("database %s survived its test", name)
		}
	}
}

func TestMigrated_AppliesTheSchema(t *testing.T) {
	pool, _ := Migrated(t)
	var present bool
	if err := pool.QueryRow(context.Background(), "SELECT to_regclass('platform.rate_limit') IS NOT NULL").Scan(&present); err != nil {
		t.Fatal(err)
	}
	if !present {
		t.Error("platform.rate_limit is missing after Migrated")
	}
}

func TestBaseURL_DefaultsToTheComposeServer(t *testing.T) {
	if os.Getenv("TEST_DATABASE_URL") != "" {
		t.Skip("TEST_DATABASE_URL overrides the default")
	}
	if baseURL() != defaultURL {
		t.Errorf("baseURL = %q", baseURL())
	}
}
```

- [ ] **Step 3: Write the failing db tests**

Create `apps/server/internal/db/export_test.go`:

```go
package db

import "time"

// MigrationsFS exposes the embedded migrations to the external test package.
var MigrationsFS = migrationsFS

// SetRetryDelay replaces the connect back-off for a test and returns a restore func.
func SetRetryDelay(f func(attempt int) time.Duration) (restore func()) {
	previous := retryDelay
	retryDelay = f
	return func() { retryDelay = previous }
}
```

Create `apps/server/internal/db/db_test.go`:

```go
package db_test

import (
	"context"
	"io/fs"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/vantigo-io/vantigo/server/internal/db"
	"github.com/vantigo-io/vantigo/server/internal/testdb"
)

func tableExists(t *testing.T, databaseURL, table string) bool {
	t.Helper()
	ctx := context.Background()
	conn, err := pgx.Connect(ctx, databaseURL)
	if err != nil {
		t.Fatalf("connect: %v", err)
	}
	defer func() { _ = conn.Close(ctx) }()
	var present bool
	if err := conn.QueryRow(ctx, "SELECT to_regclass($1) IS NOT NULL", table).Scan(&present); err != nil {
		t.Fatalf("to_regclass: %v", err)
	}
	return present
}

func TestApplyMigrations_CreatesThePlatformSchema(t *testing.T) {
	url := testdb.URL(t)
	if err := db.ApplyMigrations(context.Background(), url); err != nil {
		t.Fatalf("ApplyMigrations: %v", err)
	}
	if !tableExists(t, url, "platform.rate_limit") {
		t.Error("platform.rate_limit was not created")
	}
}

func TestApplyMigrations_IsIdempotent(t *testing.T) {
	url := testdb.URL(t)
	for i := range 2 {
		if err := db.ApplyMigrations(context.Background(), url); err != nil {
			t.Fatalf("run %d: %v", i+1, err)
		}
	}
}

// A second migrator must wait on the advisory lock rather than race the first.
// pg_stat_activity is the ground truth for "waiting on the lock", not a sleep.
func TestApplyMigrations_WaitsForTheAdvisoryLock(t *testing.T) {
	url := testdb.URL(t)
	ctx := context.Background()

	holder, err := pgx.Connect(ctx, url)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = holder.Close(ctx) }()
	if _, err := holder.Exec(ctx, "SELECT pg_advisory_lock($1)", db.MigrationLockKey); err != nil {
		t.Fatal(err)
	}

	done := make(chan error, 1)
	go func() { done <- db.ApplyMigrations(ctx, url) }()

	deadline := time.Now().Add(10 * time.Second)
	for {
		var waiting int
		err := holder.QueryRow(ctx, `SELECT count(*) FROM pg_stat_activity
			WHERE datname = current_database() AND wait_event_type = 'Lock' AND wait_event = 'advisory'`).Scan(&waiting)
		if err != nil {
			t.Fatal(err)
		}
		if waiting > 0 {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("the migrator never waited on the advisory lock")
		}
		time.Sleep(20 * time.Millisecond)
	}

	select {
	case err := <-done:
		t.Fatalf("ApplyMigrations finished while the lock was held: %v", err)
	default:
	}
	if tableExists(t, url, "platform.rate_limit") {
		t.Fatal("migrations ran while another session held the lock")
	}

	if _, err := holder.Exec(ctx, "SELECT pg_advisory_unlock($1)", db.MigrationLockKey); err != nil {
		t.Fatal(err)
	}
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("ApplyMigrations: %v", err)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("ApplyMigrations did not finish after the lock was released")
	}
	if !tableExists(t, url, "platform.rate_limit") {
		t.Error("platform.rate_limit is missing after the waiting migrator ran")
	}
}

func TestOpen_ReturnsAPingedPool(t *testing.T) {
	pool, err := db.Open(context.Background(), testdb.URL(t))
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer pool.Close()
	if err := pool.Ping(context.Background()); err != nil {
		t.Fatalf("Ping: %v", err)
	}
}

func TestOpen_GivesUpOnAnUnreachableServer(t *testing.T) {
	defer db.SetRetryDelay(func(int) time.Duration { return 0 })()

	_, err := db.Open(context.Background(), "postgres://nobody:secret@127.0.0.1:1/none?sslmode=disable&connect_timeout=1")
	if err == nil || !strings.Contains(err.Error(), "unreachable after 6 attempts") {
		t.Fatalf("err = %v, want it to give up after 6 attempts", err)
	}
}

var migrationName = regexp.MustCompile(`^\d{5}_[a-z]+_[a-z0-9_]+\.sql$`)

func TestMigrationFilesFollowTheNamingRule(t *testing.T) {
	entries, err := fs.ReadDir(db.MigrationsFS, "migrations")
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) == 0 {
		t.Fatal("no embedded migrations")
	}
	for _, e := range entries {
		if !migrationName.MatchString(e.Name()) {
			t.Errorf("%s does not match NNNNN_<owner>_<name>.sql", e.Name())
		}
	}
}
```

- [ ] **Step 4: Run the tests to verify they fail**

Run: `cd apps/server && go test ./internal/db/ ./internal/testdb/`
Expected: FAIL to compile with `undefined: db.Open`, `undefined: URL`, and similar.

- [ ] **Step 5: Implement the database package**

Create `apps/server/internal/db/migrations/00001_platform_init.sql`:

```sql
-- +goose Up
-- The platform schema holds process-wide infrastructure owned by no module.
-- Each module's baseline migration creates that module's own schema.
CREATE SCHEMA platform;

-- Fixed-window rate-limit counters (internal/ratelimit). One row per
-- policy-and-client key, reset in place when a new window starts, so the
-- table grows with distinct clients rather than with traffic.
CREATE TABLE platform.rate_limit (
    key          text        PRIMARY KEY,
    window_start timestamptz NOT NULL,
    hits         integer     NOT NULL CHECK (hits > 0)
);

-- +goose Down
DROP TABLE platform.rate_limit;
DROP SCHEMA platform;
```

Create `apps/server/internal/db/db.go`:

```go
// Package db owns PostgreSQL access: the request-traffic pool, the embedded
// goose migrations, and the advisory-lock-guarded runner that applies them.
package db

import (
	"context"
	"fmt"
	"log/slog"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
)

// Open returns a connection pool for request traffic. It pings before
// returning, so a wrong DATABASE_URL fails startup instead of the first
// request.
func Open(ctx context.Context, databaseURL string) (*pgxpool.Pool, error) {
	cfg, err := pgxpool.ParseConfig(databaseURL)
	if err != nil {
		return nil, fmt.Errorf("db: parse connection string: %w", err)
	}
	pool, err := pgxpool.NewWithConfig(ctx, cfg)
	if err != nil {
		return nil, fmt.Errorf("db: open pool: %w", err)
	}
	if err := waitForDatabase(ctx, pool.Ping); err != nil {
		pool.Close()
		return nil, err
	}
	return pool, nil
}

// connectAttempts and retryDelay bound how long startup waits for a PostgreSQL
// that is still coming up (a compose stack starting both at once): six
// attempts with a growing pause, about 30 s in total.
const connectAttempts = 6

var retryDelay = func(attempt int) time.Duration { return time.Duration(attempt) * 2 * time.Second }

func waitForDatabase(ctx context.Context, ping func(context.Context) error) error {
	var err error
	for attempt := 1; attempt <= connectAttempts; attempt++ {
		if err = ping(ctx); err == nil {
			return nil
		}
		if attempt == connectAttempts {
			break
		}
		slog.WarnContext(ctx, "PostgreSQL is not reachable yet; retrying", "attempt", attempt, "error", err)
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(retryDelay(attempt)):
		}
	}
	return fmt.Errorf("db: PostgreSQL unreachable after %d attempts: %w", connectAttempts, err)
}
```

Create `apps/server/internal/db/migrate.go`:

```go
package db

import (
	"context"
	"database/sql"
	"embed"
	"fmt"
	"io/fs"
	"log/slog"

	_ "github.com/jackc/pgx/v5/stdlib" // registers the "pgx" database/sql driver
	"github.com/pressly/goose/v3"
)

// MigrationLockKey serialises every migrator of every version ("VANTIGO1" as
// a 64-bit value — the key the .NET MigrationLock used). It must never change:
// an old and a new binary mid-rollout would stop contending with each other,
// silently.
const MigrationLockKey int64 = 0x56414E5449474F31

//go:embed migrations/*.sql
var migrationsFS embed.FS

// ApplyMigrations applies every pending migration under the advisory lock
// and returns an error rather than exiting, so both `migrate` and `api`
// (migrate, then serve) decide for themselves what a failure means.
//
// pg_advisory_lock is held by a session, so the lock, the migrations and the
// unlock must share one physical connection. database/sql is a pool; pinning
// it to a single connection is what makes every statement below run in the
// session that holds the lock.
func ApplyMigrations(ctx context.Context, databaseURL string) error {
	sqlDB, err := sql.Open("pgx", databaseURL)
	if err != nil {
		return fmt.Errorf("db: open: %w", err)
	}
	defer func() { _ = sqlDB.Close() }()
	sqlDB.SetMaxOpenConns(1)
	sqlDB.SetMaxIdleConns(1)

	if err := waitForDatabase(ctx, sqlDB.PingContext); err != nil {
		return err
	}

	slog.InfoContext(ctx, "acquiring the migration advisory lock")
	if _, err := sqlDB.ExecContext(ctx, "SELECT pg_advisory_lock($1)", MigrationLockKey); err != nil {
		return fmt.Errorf("db: acquire the migration lock: %w", err)
	}
	defer func() {
		_, _ = sqlDB.ExecContext(context.WithoutCancel(ctx), "SELECT pg_advisory_unlock($1)", MigrationLockKey)
	}()

	dir, err := fs.Sub(migrationsFS, "migrations")
	if err != nil {
		return fmt.Errorf("db: embedded migrations: %w", err)
	}
	provider, err := goose.NewProvider(goose.DialectPostgres, sqlDB, dir)
	if err != nil {
		return fmt.Errorf("db: goose provider: %w", err)
	}
	results, err := provider.Up(ctx)
	if err != nil {
		return fmt.Errorf("db: apply migrations: %w", err)
	}
	for _, r := range results {
		slog.InfoContext(ctx, "applied migration", "version", r.Source.Version, "file", r.Source.Path, "duration", r.Duration)
	}
	slog.InfoContext(ctx, "migrations are up to date", "applied", len(results))
	return nil
}
```

- [ ] **Step 6: Implement testdb**

Create `apps/server/internal/testdb/testdb.go`:

```go
// Package testdb gives every test its own empty PostgreSQL database, created
// on the server named by TEST_DATABASE_URL (default: docker-compose.test.yml's
// server) and dropped when the test ends. Tests never share tables, so
// packages run in parallel and nothing needs truncating.
package testdb

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"net/url"
	"os"
	"testing"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/vantigo-io/vantigo/server/internal/db"
)

const defaultURL = "postgres://vantigo:vantigo@127.0.0.1:55432/vantigo_test?sslmode=disable"

// baseURL is the server tests create databases on. It must be URL-form and
// its role must be allowed to CREATE DATABASE.
func baseURL() string {
	if v := os.Getenv("TEST_DATABASE_URL"); v != "" {
		return v
	}
	return defaultURL
}

// URL creates a fresh, empty database and returns its connection string. The
// database is dropped (WITH (FORCE), so leaked connections cannot block it)
// when the test ends.
func URL(t testing.TB) string {
	t.Helper()
	base := baseURL()
	parsed, err := url.Parse(base)
	if err != nil {
		t.Fatalf("testdb: TEST_DATABASE_URL is not a URL: %v", err)
	}

	ctx := context.Background()
	admin, err := pgx.Connect(ctx, base)
	if err != nil {
		t.Fatalf("testdb: cannot reach the test database server %s: %v\n"+
			"Start it with `docker compose -f docker-compose.test.yml up -d --wait`, or point TEST_DATABASE_URL at another server.",
			parsed.Redacted(), err)
	}
	defer func() { _ = admin.Close(ctx) }()

	var b [8]byte
	_, _ = rand.Read(b[:])
	name := "t_" + hex.EncodeToString(b[:])
	if _, err := admin.Exec(ctx, "CREATE DATABASE "+name); err != nil {
		t.Fatalf("testdb: create database: %v", err)
	}
	t.Cleanup(func() {
		ctx := context.Background()
		conn, err := pgx.Connect(ctx, base)
		if err != nil {
			t.Errorf("testdb: drop %s: %v", name, err)
			return
		}
		defer func() { _ = conn.Close(ctx) }()
		if _, err := conn.Exec(ctx, "DROP DATABASE IF EXISTS "+name+" WITH (FORCE)"); err != nil {
			t.Errorf("testdb: drop %s: %v", name, err)
		}
	})

	parsed.Path = "/" + name
	return parsed.String()
}

// Migrated creates a fresh database, applies every migration, and returns a
// pool on it (closed when the test ends) together with its connection string.
func Migrated(t testing.TB) (*pgxpool.Pool, string) {
	t.Helper()
	databaseURL := URL(t)
	ctx := context.Background()
	if err := db.ApplyMigrations(ctx, databaseURL); err != nil {
		t.Fatalf("testdb: migrate: %v", err)
	}
	pool, err := db.Open(ctx, databaseURL)
	if err != nil {
		t.Fatalf("testdb: open pool: %v", err)
	}
	t.Cleanup(pool.Close)
	return pool, databaseURL
}
```

- [ ] **Step 7: Add the dependency and run the tests**

Run: `cd apps/server && go get github.com/pressly/goose/v3@v3.28.0 && go mod tidy && go test ./internal/db/ ./internal/testdb/ -v -count=1 && golangci-lint run`
Expected: every test PASS (the unreachable-server test takes about 6 × `connect_timeout`, a few seconds); `0 issues.`

- [ ] **Step 8: Commit**

```bash
git add docker-compose.test.yml apps/server/go.mod apps/server/go.sum apps/server/internal/db apps/server/internal/testdb
git commit -m "feat(server): add the database pool, migrations and test databases

Embedded goose migrations run under the same advisory lock key as the .NET
MigrationLock, startup waits for PostgreSQL to come up, and every test gets
its own throwaway database on docker-compose.test.yml's server."
```

---

### Task 7: PostgreSQL-backed rate limiter

**Files:**
- Create: `apps/server/internal/ratelimit/ratelimit.go`
- Test: `apps/server/internal/ratelimit/ratelimit_test.go`

**Interfaces:**
- Consumes: `platform.rate_limit` (Task 6), `testdb.Migrated` (Task 6), `httpx.ClientIP`, `httpx.WriteError` (Task 3).
- Produces:
  - `type Policy struct { Name string; Limit int; Window time.Duration }`
  - `type Decision struct { Allowed bool; RetryAfter time.Duration }`
  - `func New(pool *pgxpool.Pool) *Limiter`
  - `func (l *Limiter) Allow(ctx context.Context, p Policy, client string) (Decision, error)`
  - `func (l *Limiter) Middleware(p Policy) func(http.Handler) http.Handler` — keyed by `httpx.ClientIP`; rejects with 429, `Retry-After`, and `{"error":{"code":"rate_limited"}}` (the .NET rejection shape)

Counters live in PostgreSQL rather than process memory, so a limit holds across replicas and restarts. Windows are fixed and aligned to the clock (`now.Truncate(window)`). Identity (sub-project 3) defines the named policies.

- [ ] **Step 1: Write the failing tests**

Create `apps/server/internal/ratelimit/ratelimit_test.go`:

```go
package ratelimit

import (
	"context"
	"net/http"
	"net/http/httptest"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/vantigo-io/vantigo/server/internal/testdb"
)

var login = Policy{Name: "login", Limit: 2, Window: time.Minute}

// newLimiter returns a limiter on a fresh database with a controllable clock
// starting 10 s into a minute window.
func newLimiter(t *testing.T) (*Limiter, *time.Time) {
	t.Helper()
	pool, _ := testdb.Migrated(t)
	now := time.Date(2026, 9, 10, 12, 0, 10, 0, time.UTC)
	l := New(pool)
	l.now = func() time.Time { return now }
	return l, &now
}

func mustAllow(t *testing.T, l *Limiter, p Policy, client string) Decision {
	t.Helper()
	d, err := l.Allow(context.Background(), p, client)
	if err != nil {
		t.Fatalf("Allow: %v", err)
	}
	return d
}

func TestAllow_LimitsHitsWithinAWindow(t *testing.T) {
	l, _ := newLimiter(t)

	for i := range 2 {
		if d := mustAllow(t, l, login, "203.0.113.7"); !d.Allowed {
			t.Fatalf("hit %d rejected", i+1)
		}
	}
	d := mustAllow(t, l, login, "203.0.113.7")
	if d.Allowed {
		t.Fatal("third hit allowed with a limit of 2")
	}
	if d.RetryAfter != 50*time.Second {
		t.Errorf("RetryAfter = %v, want 50s (the rest of the window)", d.RetryAfter)
	}
}

func TestAllow_ANewWindowStartsAfresh(t *testing.T) {
	l, now := newLimiter(t)
	for range 3 {
		mustAllow(t, l, login, "203.0.113.7")
	}
	*now = now.Add(time.Minute)
	if d := mustAllow(t, l, login, "203.0.113.7"); !d.Allowed {
		t.Error("first hit of the next window was rejected")
	}
}

func TestAllow_ClientsAndPoliciesAreCountedSeparately(t *testing.T) {
	l, _ := newLimiter(t)
	for range 3 {
		mustAllow(t, l, login, "203.0.113.7")
	}
	if !mustAllow(t, l, login, "198.51.100.1").Allowed {
		t.Error("another client was limited by the first client's hits")
	}
	other := Policy{Name: "password-recovery", Limit: 1, Window: time.Minute}
	if !mustAllow(t, l, other, "203.0.113.7").Allowed {
		t.Error("another policy was limited by the login policy's hits")
	}
}

func TestAllow_IsAtomicUnderConcurrency(t *testing.T) {
	l, _ := newLimiter(t)
	p := Policy{Name: "burst", Limit: 10, Window: time.Minute}

	var allowed atomic.Int32
	var wg sync.WaitGroup
	for range 25 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			d, err := l.Allow(context.Background(), p, "203.0.113.7")
			if err != nil {
				t.Errorf("Allow: %v", err) // Errorf, not Fatalf: this is not the test goroutine
				return
			}
			if d.Allowed {
				allowed.Add(1)
			}
		}()
	}
	wg.Wait()
	if got := allowed.Load(); got != 10 {
		t.Errorf("%d concurrent hits allowed, want exactly 10", got)
	}
}

func TestAllow_RejectsAnInvalidPolicy(t *testing.T) {
	l, _ := newLimiter(t)
	for _, p := range []Policy{{Name: "", Limit: 1, Window: time.Minute}, {Name: "x", Limit: 0, Window: time.Minute}, {Name: "x", Limit: 1, Window: time.Millisecond}} {
		if _, err := l.Allow(context.Background(), p, "c"); err == nil {
			t.Errorf("policy %+v accepted", p)
		}
	}
}

func TestMiddleware_RejectsWith429AndRetryAfter(t *testing.T) {
	l, _ := newLimiter(t)
	var calls int
	h := l.Middleware(Policy{Name: "login", Limit: 1, Window: time.Minute})(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		calls++
		w.WriteHeader(http.StatusNoContent)
	}))

	serve := func(remote string) *httptest.ResponseRecorder {
		req := httptest.NewRequest(http.MethodPost, "/api/v1/identity/login", nil)
		req.RemoteAddr = remote
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, req)
		return rec
	}

	if rec := serve("203.0.113.7:1000"); rec.Code != http.StatusNoContent {
		t.Fatalf("first request: %d", rec.Code)
	}
	rec := serve("203.0.113.7:2000")
	if rec.Code != http.StatusTooManyRequests {
		t.Fatalf("second request: %d, want 429", rec.Code)
	}
	if rec.Header().Get("Retry-After") != "50" {
		t.Errorf("Retry-After = %q, want 50", rec.Header().Get("Retry-After"))
	}
	if rec.Body.String() != `{"error":{"code":"rate_limited"}}`+"\n" {
		t.Errorf("body = %q", rec.Body.String())
	}
	if calls != 1 {
		t.Errorf("handler ran %d times, want 1", calls)
	}
	if rec := serve("198.51.100.1:1000"); rec.Code != http.StatusNoContent {
		t.Errorf("a different client was rejected: %d", rec.Code)
	}
}

func TestMiddleware_PanicsOnAnInvalidPolicy(t *testing.T) {
	l, _ := newLimiter(t)
	defer func() {
		if recover() == nil {
			t.Error("Middleware accepted a zero limit")
		}
	}()
	l.Middleware(Policy{Name: "x", Limit: 0, Window: time.Minute})
}
```

- [ ] **Step 2: Run the tests to verify they fail**

Run: `cd apps/server && go test ./internal/ratelimit/`
Expected: FAIL to compile with `undefined: Policy`, `undefined: New`.

- [ ] **Step 3: Implement the limiter**

Create `apps/server/internal/ratelimit/ratelimit.go`:

```go
// Package ratelimit is a fixed-window rate limiter whose counters live in
// PostgreSQL (platform.rate_limit), so a limit holds across replicas and
// restarts rather than per process.
package ratelimit

import (
	"context"
	"fmt"
	"math"
	"net/http"
	"strconv"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/vantigo-io/vantigo/server/internal/httpx"
)

// Policy is a named limit: at most Limit hits per client per Window.
type Policy struct {
	Name   string
	Limit  int
	Window time.Duration
}

func (p Policy) validate() error {
	if p.Name == "" || p.Limit < 1 || p.Window < time.Second {
		return fmt.Errorf("ratelimit: invalid policy %+v (need a name, a limit of at least 1 and a window of at least 1s)", p)
	}
	return nil
}

// Decision is the outcome of one hit.
type Decision struct {
	Allowed bool
	// RetryAfter is the time left in the current window when a hit is rejected.
	RetryAfter time.Duration
}

// Limiter records hits in PostgreSQL.
type Limiter struct {
	pool *pgxpool.Pool
	now  func() time.Time
}

// New returns a limiter on pool.
func New(pool *pgxpool.Pool) *Limiter {
	return &Limiter{pool: pool, now: time.Now}
}

// hitSQL counts one hit atomically: a new key or a new window starts at 1,
// otherwise the counter increments. Concurrent hits serialise on the row.
const hitSQL = `
INSERT INTO platform.rate_limit (key, window_start, hits)
VALUES ($1, $2, 1)
ON CONFLICT (key) DO UPDATE SET
    hits = CASE WHEN platform.rate_limit.window_start = EXCLUDED.window_start
                THEN platform.rate_limit.hits + 1
                ELSE 1 END,
    window_start = EXCLUDED.window_start
RETURNING hits`

// Allow records one hit by client under p and reports whether it is within
// the limit.
func (l *Limiter) Allow(ctx context.Context, p Policy, client string) (Decision, error) {
	if err := p.validate(); err != nil {
		return Decision{}, err
	}
	now := l.now().UTC()
	windowStart := now.Truncate(p.Window)

	var hits int
	if err := l.pool.QueryRow(ctx, hitSQL, p.Name+":"+client, windowStart).Scan(&hits); err != nil {
		return Decision{}, fmt.Errorf("ratelimit: record hit: %w", err)
	}
	if hits <= p.Limit {
		return Decision{Allowed: true}, nil
	}
	return Decision{RetryAfter: windowStart.Add(p.Window).Sub(now)}, nil
}

// Middleware limits requests per client address (httpx.ClientIP, so it must
// run inside httpx.Forwarded). A counter that cannot be recorded fails the
// request closed with a 500 rather than letting it through unmetered.
func (l *Limiter) Middleware(p Policy) func(http.Handler) http.Handler {
	if err := p.validate(); err != nil {
		panic(err)
	}
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			d, err := l.Allow(r.Context(), p, httpx.ClientIP(r))
			if err != nil {
				httpx.WriteError(w, r, err)
				return
			}
			if !d.Allowed {
				seconds := int(math.Ceil(d.RetryAfter.Seconds()))
				w.Header().Set("Retry-After", strconv.Itoa(max(seconds, 1)))
				w.Header().Set("Content-Type", "application/json")
				w.WriteHeader(http.StatusTooManyRequests)
				_, _ = w.Write([]byte(`{"error":{"code":"rate_limited"}}` + "\n"))
				return
			}
			next.ServeHTTP(w, r)
		})
	}
}
```

- [ ] **Step 4: Run the tests to verify they pass**

Run: `cd apps/server && go test ./internal/ratelimit/ -v -count=1 && golangci-lint run`
Expected: every test PASS; `0 issues.`

- [ ] **Step 5: Commit**

```bash
git add apps/server/internal/ratelimit
git commit -m "feat(server): add a PostgreSQL-backed fixed-window rate limiter

Counters live in platform.rate_limit so limits hold across replicas; the
middleware answers 429 with Retry-After and the .NET rejection body."
```

---

### Task 8: Health endpoints and the healthcheck probe

**Files:**
- Create: `apps/server/internal/health/health.go`
- Test: `apps/server/internal/health/health_test.go`

**Interfaces:**
- Consumes: nothing from earlier tasks.
- Produces:
  - `type Check struct { Name string; Run func(context.Context) error }` — `pool.Ping` fits `Run` directly
  - `func Handler(logger *slog.Logger, version string, checks ...Check) http.Handler` — serves `GET /health/live` and `GET /health/ready`
  - `func Probe(ctx context.Context, baseURL string) error` — one GET of `baseURL + "/health/ready"`, 5 s timeout

`/health/live` depends on nothing. `/health/ready` runs every check concurrently, each with a 3 s timeout, and answers 503 listing the failing checks. Bodies are JSON: `{"status":"healthy","version":"…"}`, or `{"status":"unhealthy","version":"…","failing":["postgres"]}`.

- [ ] **Step 1: Write the failing tests**

Create `apps/server/internal/health/health_test.go`:

```go
package health

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"slices"
	"testing"
	"time"
)

type report struct {
	Status  string   `json:"status"`
	Version string   `json:"version"`
	Failing []string `json:"failing"`
}

func probe(t *testing.T, h http.Handler, method, path string) (int, report, http.Header) {
	t.Helper()
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(method, path, nil))
	var r report
	if rec.Code != http.StatusMethodNotAllowed {
		if err := json.Unmarshal(rec.Body.Bytes(), &r); err != nil {
			t.Fatalf("decode %s: %v (%q)", path, err, rec.Body.String())
		}
	}
	return rec.Code, r, rec.Header()
}

var (
	ok     = Check{Name: "postgres", Run: func(context.Context) error { return nil }}
	broken = func(name string) Check {
		return Check{Name: name, Run: func(context.Context) error { return errors.New("down") }}
	}
)

func TestLive_DependsOnNothing(t *testing.T) {
	h := Handler(slog.New(slog.DiscardHandler), "1.2.3", broken("postgres"))
	code, r, hdr := probe(t, h, http.MethodGet, "/health/live")
	if code != http.StatusOK || r.Status != "healthy" || r.Version != "1.2.3" {
		t.Errorf("live = %d %+v", code, r)
	}
	if hdr.Get("Cache-Control") != "no-store" {
		t.Errorf("Cache-Control = %q", hdr.Get("Cache-Control"))
	}
}

func TestReady_HealthyWhenEveryCheckPasses(t *testing.T) {
	code, r, _ := probe(t, Handler(slog.New(slog.DiscardHandler), "1.2.3", ok), http.MethodGet, "/health/ready")
	if code != http.StatusOK || r.Status != "healthy" || len(r.Failing) != 0 {
		t.Errorf("ready = %d %+v", code, r)
	}
}

func TestReady_ListsEveryFailingCheck(t *testing.T) {
	h := Handler(slog.New(slog.DiscardHandler), "1.2.3", broken("storage"), ok, broken("postgres"))
	code, r, _ := probe(t, h, http.MethodGet, "/health/ready")
	if code != http.StatusServiceUnavailable || r.Status != "unhealthy" {
		t.Errorf("ready = %d %+v", code, r)
	}
	if !slices.Equal(r.Failing, []string{"postgres", "storage"}) {
		t.Errorf("failing = %v, want [postgres storage]", r.Failing)
	}
}

func TestReady_AHangingCheckTimesOut(t *testing.T) {
	previous := checkTimeout
	checkTimeout = 50 * time.Millisecond
	defer func() { checkTimeout = previous }()

	hanging := Check{Name: "postgres", Run: func(ctx context.Context) error { <-ctx.Done(); return ctx.Err() }}
	start := time.Now()
	code, _, _ := probe(t, Handler(slog.New(slog.DiscardHandler), "1.2.3", hanging), http.MethodGet, "/health/ready")
	if code != http.StatusServiceUnavailable || time.Since(start) > 2*time.Second {
		t.Errorf("status %d after %v", code, time.Since(start))
	}
}

func TestHandler_OnlyAnswersGET(t *testing.T) {
	if code, _, _ := probe(t, Handler(slog.New(slog.DiscardHandler), "1.2.3"), http.MethodPost, "/health/live"); code != http.StatusMethodNotAllowed {
		t.Errorf("POST /health/live = %d, want 405", code)
	}
}

func TestProbe(t *testing.T) {
	status := http.StatusOK
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/health/ready" {
			t.Errorf("probed %s", r.URL.Path)
		}
		w.WriteHeader(status)
	}))
	defer srv.Close()

	if err := Probe(context.Background(), srv.URL); err != nil {
		t.Errorf("healthy server: %v", err)
	}
	status = http.StatusServiceUnavailable
	if err := Probe(context.Background(), srv.URL); err == nil {
		t.Error("503 reported as healthy")
	}
	srv.Close()
	if err := Probe(context.Background(), srv.URL); err == nil {
		t.Error("closed server reported as healthy")
	}
}
```

- [ ] **Step 2: Run the tests to verify they fail**

Run: `cd apps/server && go test ./internal/health/`
Expected: FAIL to compile with `undefined: Handler`, `undefined: Check`, `undefined: checkTimeout`.

- [ ] **Step 3: Implement health**

Create `apps/server/internal/health/health.go`:

```go
// Package health serves the two container probes and the client the
// `healthcheck` command uses to call them.
package health

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"slices"
	"time"
)

// Check is one readiness dependency. pgxpool.Pool.Ping fits Run as is.
type Check struct {
	Name string
	Run  func(context.Context) error
}

// checkTimeout bounds each readiness check so a wedged dependency answers 503
// instead of hanging the probe. A variable so tests can shorten it.
var checkTimeout = 3 * time.Second

type report struct {
	Status  string   `json:"status"`
	Version string   `json:"version"`
	Failing []string `json:"failing,omitempty"`
}

// Handler serves GET /health/live — 200 whenever the process can answer, with
// no dependency at all — and GET /health/ready — 200 when every check passes,
// 503 naming the ones that failed. Both are anonymous and uncached.
func Handler(logger *slog.Logger, version string, checks ...Check) http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /health/live", func(w http.ResponseWriter, _ *http.Request) {
		write(w, http.StatusOK, report{Status: "healthy", Version: version})
	})
	mux.HandleFunc("GET /health/ready", func(w http.ResponseWriter, r *http.Request) {
		if failing := run(r.Context(), logger, checks); len(failing) > 0 {
			write(w, http.StatusServiceUnavailable, report{Status: "unhealthy", Version: version, Failing: failing})
			return
		}
		write(w, http.StatusOK, report{Status: "healthy", Version: version})
	})
	return mux
}

func run(ctx context.Context, logger *slog.Logger, checks []Check) []string {
	type result struct {
		name string
		err  error
	}
	results := make(chan result, len(checks))
	for _, c := range checks {
		go func() {
			cctx, cancel := context.WithTimeout(ctx, checkTimeout)
			defer cancel()
			results <- result{c.Name, c.Run(cctx)}
		}()
	}
	var failing []string
	for range checks {
		if res := <-results; res.err != nil {
			logger.WarnContext(ctx, "readiness check failed", "check", res.name, "error", res.err)
			failing = append(failing, res.name)
		}
	}
	slices.Sort(failing)
	return failing
}

func write(w http.ResponseWriter, status int, body report) {
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(body)
}

// Probe requests baseURL/health/ready once and returns nil for a 2xx. The
// image has no shell or curl, so its HEALTHCHECK runs `vantigo healthcheck`,
// which calls this against the process in the same container.
func Probe(ctx context.Context, baseURL string) error {
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, baseURL+"/health/ready", nil)
	if err != nil {
		return err
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return err
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return fmt.Errorf("health: %s/health/ready answered %d", baseURL, resp.StatusCode)
	}
	return nil
}
```

- [ ] **Step 4: Run the tests to verify they pass**

Run: `cd apps/server && go test ./internal/health/ -v -count=1 && golangci-lint run`
Expected: every test PASS; `0 issues.`

- [ ] **Step 5: Commit**

```bash
git add apps/server/internal/health
git commit -m "feat(server): add /health/live, /health/ready and the probe client"
```

---

### Task 9: Assemble the production handler

**Files:**
- Create: `apps/server/internal/server/server.go`
- Test: `apps/server/internal/server/server_test.go`

**Interfaces:**
- Consumes: `config.Config` (Task 2); `httpx.*` (Task 3); `security.Headers`, `security.HostFilter`, `security.AllowedHosts` (Task 4); `web.Handler`, `web.Index` (Task 5); `health.Handler`, `health.Check` (Task 8).
- Produces:
  - `type Options struct { Config *config.Config; Logger *slog.Logger; Index *web.Index; Assets fs.FS; Health http.Handler; API http.Handler }`
  - `func New(o Options) http.Handler`

Middleware order, outermost first: `Forwarded` → `RequestID` → `RequestLog` → `Recover` → `security.Headers` → `HostFilter` → `StripBasePath` → mux. The mux routes `/health/` → health, `/api` and `/api/` → `CrossOriginProtection` → API (or the 404 catch-all), and everything else → SPA. Security headers go outside host filtering and recovery, so rejections and 500s carry them too.

- [ ] **Step 1: Write the failing tests**

Create `apps/server/internal/server/server_test.go`:

```go
package server

import (
	"bytes"
	"context"
	"encoding/json"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"testing/fstest"
	"time"

	"github.com/vantigo-io/vantigo/server/internal/config"
	"github.com/vantigo-io/vantigo/server/internal/health"
	"github.com/vantigo-io/vantigo/server/internal/httpx"
	"github.com/vantigo-io/vantigo/server/internal/web"
)

const indexHTML = `<!doctype html><html><head><title>Vantigo</title>` +
	`<script type="module" src="/assets/app-1.js"></script></head><body><div id="root"></div></body></html>`

type fixture struct {
	handler http.Handler
	index   *web.Index
	logs    *bytes.Buffer
}

func newFixture(t *testing.T, mutate func(*config.Config, *Options)) fixture {
	t.Helper()
	cfg := &config.Config{
		Env:             config.Production,
		AppOrigin:       "https://vantigo.example.com",
		AppHostname:     "vantigo.example.com",
		Port:            8080,
		ShutdownTimeout: time.Second,
		LogLevel:        slog.LevelInfo,
	}
	assets := fstest.MapFS{
		"index.html":       {Data: []byte(indexHTML)},
		"assets/app-1.js": {Data: []byte("console.log(1)")},
	}

	api := http.NewServeMux()
	api.HandleFunc("GET /api/v1/ping", func(w http.ResponseWriter, r *http.Request) { _, _ = w.Write([]byte("pong:" + r.URL.Path)) })
	api.HandleFunc("POST /api/v1/ping", func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusNoContent) })
	api.HandleFunc("GET /api/v1/panic", func(http.ResponseWriter, *http.Request) { panic("boom: secret detail") })
	api.HandleFunc("/", httpx.NotFound)

	var logs bytes.Buffer
	logger := slog.New(slog.NewJSONHandler(&logs, nil))
	o := Options{
		Config: cfg,
		Logger: logger,
		Assets: assets,
		Health: health.Handler(logger, "test", health.Check{Name: "postgres", Run: func(context.Context) error { return nil }}),
		API:    api,
	}
	if mutate != nil {
		mutate(cfg, &o)
	}
	idx, err := web.NewIndex(assets, cfg.BasePath, cfg.Branding)
	if err != nil {
		t.Fatal(err)
	}
	o.Index = idx
	return fixture{handler: New(o), index: idx, logs: &logs}
}

func (f fixture) do(req *http.Request) *httptest.ResponseRecorder {
	rec := httptest.NewRecorder()
	f.handler.ServeHTTP(rec, req)
	return rec
}

func request(method, target string, headers ...string) *http.Request {
	req := httptest.NewRequest(method, target, nil)
	for i := 0; i+1 < len(headers); i += 2 {
		req.Header.Set(headers[i], headers[i+1])
	}
	return req
}

func TestNew_ServesTheSPAWithTheSecurityHeaders(t *testing.T) {
	f := newFixture(t, nil)
	rec := f.do(request(http.MethodGet, "https://vantigo.example.com/customers/42"))

	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), "window.__VANTIGO_APP__") {
		t.Fatalf("status %d body %q", rec.Code, rec.Body.String())
	}
	if csp := rec.Header().Get("Content-Security-Policy"); !strings.Contains(csp, "script-src 'self' "+f.index.InlineScriptHash) {
		t.Errorf("CSP does not allow the index script: %q", csp)
	}
	for name, want := range map[string]string{
		"X-Frame-Options":           "DENY",
		"X-Content-Type-Options":    "nosniff",
		"Strict-Transport-Security": "max-age=2592000",
	} {
		if got := rec.Header().Get(name); got != want {
			t.Errorf("%s = %q, want %q", name, got, want)
		}
	}
}

func TestNew_HealthRoutesToTheHealthHandler(t *testing.T) {
	rec := newFixture(t, nil).do(request(http.MethodGet, "https://vantigo.example.com/health/ready"))
	if rec.Code != http.StatusOK || rec.Header().Get("Content-Type") != "application/json" {
		t.Errorf("status %d Content-Type %q", rec.Code, rec.Header().Get("Content-Type"))
	}
}

func TestNew_UnknownAPIPathsAreProblemsNeverTheSPA(t *testing.T) {
	f := newFixture(t, func(_ *config.Config, o *Options) { o.API = nil })
	for _, path := range []string{"/api", "/api/", "/api/v1/customers"} {
		rec := f.do(request(http.MethodGet, "https://vantigo.example.com"+path))
		if rec.Code != http.StatusNotFound || rec.Header().Get("Content-Type") != "application/problem+json" {
			t.Errorf("%s: status %d Content-Type %q", path, rec.Code, rec.Header().Get("Content-Type"))
		}
	}
}

func TestNew_RoutesAPIRequestsWithTheirFullPath(t *testing.T) {
	rec := newFixture(t, nil).do(request(http.MethodGet, "https://vantigo.example.com/api/v1/ping"))
	if rec.Body.String() != "pong:/api/v1/ping" {
		t.Errorf("body = %q", rec.Body.String())
	}
}

func TestNew_RejectsForeignHostsButNotLoopback(t *testing.T) {
	f := newFixture(t, nil)
	if rec := f.do(request(http.MethodGet, "https://evil.example/")); rec.Code != http.StatusBadRequest {
		t.Errorf("foreign host: %d, want 400", rec.Code)
	}
	if rec := f.do(request(http.MethodGet, "http://127.0.0.1:8080/health/ready")); rec.Code != http.StatusOK {
		t.Errorf("loopback probe: %d, want 200", rec.Code)
	}
}

func TestNew_CrossOriginProtection(t *testing.T) {
	f := newFixture(t, nil)
	tests := []struct {
		name string
		req  *http.Request
		want int
	}{
		{"cross-site browser POST", request(http.MethodPost, "https://vantigo.example.com/api/v1/ping", "Origin", "https://evil.example", "Sec-Fetch-Site", "cross-site"), http.StatusForbidden},
		{"same-origin browser POST", request(http.MethodPost, "https://vantigo.example.com/api/v1/ping", "Origin", "https://vantigo.example.com", "Sec-Fetch-Site", "same-origin"), http.StatusNoContent},
		{"non-browser client without either header", request(http.MethodPost, "https://vantigo.example.com/api/v1/ping"), http.StatusNoContent},
		{"APP_URL origin through a proxy that rewrote Host", request(http.MethodPost, "http://localhost:8080/api/v1/ping", "Origin", "https://vantigo.example.com"), http.StatusNoContent},
		{"cross-site GET is safe", request(http.MethodGet, "https://vantigo.example.com/api/v1/ping", "Origin", "https://evil.example", "Sec-Fetch-Site", "cross-site"), http.StatusOK},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			rec := f.do(tc.req)
			if rec.Code != tc.want {
				t.Errorf("status %d, want %d (%s)", rec.Code, tc.want, rec.Body.String())
			}
			if tc.want == http.StatusForbidden && rec.Header().Get("Content-Type") != "application/problem+json" {
				t.Errorf("rejection is not a problem document")
			}
		})
	}
}

func TestNew_BasePathMountsTheApplication(t *testing.T) {
	f := newFixture(t, func(cfg *config.Config, _ *Options) { cfg.BasePath = "/vantigo" })

	if rec := f.do(request(http.MethodGet, "https://vantigo.example.com/vantigo/")); !strings.Contains(rec.Body.String(), `src="/vantigo/assets/app-1.js"`) {
		t.Errorf("index under the base path: %d %q", rec.Code, rec.Body.String())
	}
	if rec := f.do(request(http.MethodGet, "https://vantigo.example.com/vantigo/api/v1/ping")); rec.Body.String() != "pong:/api/v1/ping" {
		t.Errorf("API under the base path: %q", rec.Body.String())
	}
	if rec := f.do(request(http.MethodGet, "http://127.0.0.1:8080/health/ready")); rec.Code != http.StatusOK {
		t.Errorf("unprefixed probe: %d", rec.Code)
	}
}

func TestNew_HSTS(t *testing.T) {
	dev := newFixture(t, func(cfg *config.Config, _ *Options) { cfg.Env = config.Development })
	if got := dev.do(request(http.MethodGet, "https://vantigo.example.com/")).Header().Get("Strict-Transport-Security"); got != "" {
		t.Errorf("development sent HSTS %q", got)
	}

	proxied := newFixture(t, func(cfg *config.Config, _ *Options) { cfg.TrustedProxyHops = 1 })
	req := request(http.MethodGet, "http://vantigo.example.com/", "X-Forwarded-Proto", "https", "X-Forwarded-For", "203.0.113.7")
	if got := proxied.do(req).Header().Get("Strict-Transport-Security"); got != "max-age=2592000" {
		t.Errorf("https behind a trusted proxy: HSTS %q", got)
	}
}

func TestNew_APIPanicIsASanitisedProblemCorrelatedWithTheLog(t *testing.T) {
	f := newFixture(t, nil)
	rec := f.do(request(http.MethodGet, "https://vantigo.example.com/api/v1/panic"))

	var p httpx.Problem
	if err := json.Unmarshal(rec.Body.Bytes(), &p); err != nil {
		t.Fatalf("decode: %v (%q)", err, rec.Body.String())
	}
	if rec.Code != http.StatusInternalServerError || p.Detail != httpx.UnexpectedErrorDetail {
		t.Errorf("status %d problem %+v", rec.Code, p)
	}
	if strings.Contains(rec.Body.String(), "secret detail") {
		t.Error("response leaks the panic value")
	}
	if rec.Header().Get("Content-Security-Policy") == "" {
		t.Error("the 500 response lost the security headers")
	}
	if !strings.Contains(f.logs.String(), "secret detail") || !strings.Contains(f.logs.String(), p.TraceID) {
		t.Errorf("log does not carry the panic and trace id %s: %s", p.TraceID, f.logs.String())
	}
}
```

- [ ] **Step 2: Run the tests to verify they fail**

Run: `cd apps/server && go test ./internal/server/`
Expected: FAIL to compile with `undefined: Options`, `undefined: New`.

- [ ] **Step 3: Implement the assembly**

Create `apps/server/internal/server/server.go`:

```go
// Package server assembles the production HTTP handler from the platform
// packages. cmd/vantigo builds the Options; tests drive the whole stack
// through New exactly as production runs it.
package server

import (
	"io/fs"
	"log/slog"
	"net/http"

	"github.com/vantigo-io/vantigo/server/internal/config"
	"github.com/vantigo-io/vantigo/server/internal/httpx"
	"github.com/vantigo-io/vantigo/server/internal/security"
	"github.com/vantigo-io/vantigo/server/internal/web"
)

// Options are the collaborators the handler is assembled from.
type Options struct {
	Config *config.Config
	Logger *slog.Logger
	Index  *web.Index
	Assets fs.FS
	Health http.Handler
	// API serves everything under /api/ and receives the full path. Nil
	// answers every API path with a 404 problem.
	API http.Handler
}

// New returns the process's HTTP handler.
func New(o Options) http.Handler {
	api := o.API
	if api == nil {
		api = http.HandlerFunc(httpx.NotFound)
	}

	// CSRF: unsafe cross-site browser requests are rejected (Sec-Fetch-Site,
	// falling back to Origin against Host). APP_URL is trusted explicitly so a
	// proxy that rewrites Host does not turn same-origin requests into
	// rejections. Requests with neither header are non-browser clients and
	// pass. Session cookies are SameSite=Strict as the second layer.
	csrf := http.NewCrossOriginProtection()
	if err := csrf.AddTrustedOrigin(o.Config.AppOrigin); err != nil {
		panic("server: APP_URL is not a valid origin: " + err.Error()) // config.Load already validated it
	}
	csrf.SetDenyHandler(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		httpx.WriteProblem(w, r, http.StatusForbidden, "Cross-origin requests are not allowed.")
	}))
	apiHandler := csrf.Handler(api)

	mux := http.NewServeMux()
	mux.Handle("/health/", o.Health)
	mux.Handle("/api/", apiHandler)
	mux.Handle("/api", apiHandler)
	mux.Handle("/", web.Handler(o.Assets, o.Index))

	return httpx.Chain(mux,
		httpx.Forwarded(o.Config.TrustedProxyHops),
		httpx.RequestID,
		httpx.RequestLog(o.Logger),
		httpx.Recover(o.Logger),
		security.Headers(security.HeaderOptions{
			InlineScriptHash: o.Index.InlineScriptHash,
			ReportOnly:       o.Config.CSPReportOnly,
			HSTS:             !o.Config.IsDevelopment(),
		}),
		security.HostFilter(security.AllowedHosts(o.Config.AppHostname)),
		httpx.StripBasePath(o.Config.BasePath),
	)
}
```

- [ ] **Step 4: Run the tests to verify they pass**

Run: `cd apps/server && go test ./internal/server/ -v -count=1 && golangci-lint run`
Expected: every test PASS; `0 issues.`

- [ ] **Step 5: Commit**

```bash
git add apps/server/internal/server
git commit -m "feat(server): assemble the production handler

One handler: forwarded-header trust, request ids and logging, panic
recovery, security headers, host filtering, base-path mounting, then
health, the CSRF-protected /api namespace with its 404 catch-all, and the
SPA."
```

---

### Task 10: The `vantigo` command — dispatch, composition and graceful shutdown

**Files:**
- Create: `apps/server/cmd/vantigo/main.go`
- Test: `apps/server/cmd/vantigo/main_test.go`

**Interfaces:**
- Consumes: `config.FromOS`/`config.Load` (Task 2), `db.Open`, `db.ApplyMigrations` (Task 6), `web.Assets`, `web.NewIndex` (Task 5), `health.Handler`, `health.Probe` (Task 8), `server.New` (Task 9), `buildinfo.Version` (Task 1), `testdb` (Task 6).
- Produces (package `main`, used by Task 11):
  - `func run(args []string, stdout, stderr io.Writer) int` — the process exit code
  - `func serve(ctx context.Context, logger *slog.Logger, cfg *config.Config, ln net.Listener, withAPI bool) int`
  - `func serveUntilDone(ctx context.Context, logger *slog.Logger, srv *http.Server, ln net.Listener, timeout time.Duration) int`

Dispatch table (spec §3.2):

| Command | Behaviour | Exit |
|---|---|---|
| `api` | migrate under the advisory lock, then serve SPA + API + health | 0 after a clean drain; 1 on failure |
| `server` | serve SPA + API + health; never migrates | same |
| `worker` | health only (the worker runtime arrives with Communications) | same |
| `migrate` | apply migrations with `MIGRATIONS_DATABASE_URL` | 0/1 |
| `seed` | development only; nothing to seed until the modules exist | 0, or 2 outside development |
| `healthcheck` | `health.Probe` against `127.0.0.1:$PORT`; reads nothing else | 0/1 |
| none / unknown | usage on stderr | 2 |

Migration runs on `context.Background()`: a SIGTERM that arrives mid-DDL mustn't abort it. The listener opens only after migrations finish, so a probe never queues against a port that isn't serving yet.

- [ ] **Step 1: Write the failing tests**

Create `apps/server/cmd/vantigo/main_test.go`:

```go
package main

import (
	"bytes"
	"context"
	"io"
	"log/slog"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/vantigo-io/vantigo/server/internal/config"
	"github.com/vantigo-io/vantigo/server/internal/testdb"
)

// setEnv sets the variables config reads and blanks every other one it knows,
// so the developer's own environment cannot leak into a test.
func setEnv(t *testing.T, pairs ...string) {
	t.Helper()
	for _, k := range []string{"APP_ENV", "DATABASE_URL", "MIGRATIONS_DATABASE_URL", "APP_URL", "APP_BASE_PATH", "PORT",
		"TRUSTED_PROXY_HOPS", "ALLOW_INSECURE_TRANSPORT", "CSP_REPORT_ONLY", "SHUTDOWN_TIMEOUT", "LOG_LEVEL", "PGSSLMODE"} {
		t.Setenv(k, "")
	}
	for i := 0; i+1 < len(pairs); i += 2 {
		t.Setenv(pairs[i], pairs[i+1])
	}
}

func runCapture(args ...string) (int, string, string) {
	var stdout, stderr bytes.Buffer
	code := run(args, &stdout, &stderr)
	return code, stdout.String(), stderr.String()
}

func TestParseArgs(t *testing.T) {
	for args, want := range map[string]mode{
		"":            modeUsage,
		"api":         modeAPI,
		"server":      modeServer,
		"worker":      modeWorker,
		"migrate":     modeMigrate,
		"seed":        modeSeed,
		"healthcheck": modeHealthcheck,
		"migrations":  modeUsage,
		"API":         modeUsage,
	} {
		var argv []string
		if args != "" {
			argv = []string{args}
		}
		if got, _ := parseArgs(argv); got != want {
			t.Errorf("parseArgs(%q) = %v, want %v", args, got, want)
		}
	}
}

func TestRun_UsageErrorsExit2(t *testing.T) {
	code, _, stderr := runCapture()
	if code != 2 || !strings.Contains(stderr, usage) {
		t.Errorf("no command: exit %d stderr %q", code, stderr)
	}
	code, _, stderr = runCapture("bogus")
	if code != 2 || !strings.Contains(stderr, `unknown command "bogus"`) {
		t.Errorf("unknown command: exit %d stderr %q", code, stderr)
	}
}

func TestRun_InvalidConfigurationExits1WithEveryProblem(t *testing.T) {
	setEnv(t, "PORT", "0")
	code, _, stderr := runCapture("migrate")
	if code != 1 {
		t.Errorf("exit %d, want 1", code)
	}
	for _, want := range []string{"DATABASE_URL: is required", "APP_URL: is required", "PORT: must be an integer"} {
		if !strings.Contains(stderr, want) {
			t.Errorf("stderr is missing %q: %s", want, stderr)
		}
	}
}

func TestRun_SeedRefusesOutsideDevelopment(t *testing.T) {
	setEnv(t, "DATABASE_URL", "postgres://v:v@127.0.0.1:1/v", "APP_URL", "http://localhost:8080", "ALLOW_INSECURE_TRANSPORT", "1")
	if code, _, stderr := runCapture("seed"); code != 2 || !strings.Contains(stderr, "APP_ENV=development") {
		t.Errorf("exit %d stderr %q", code, stderr)
	}
}

func TestRun_Healthcheck(t *testing.T) {
	status := http.StatusOK
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(status) }))
	defer srv.Close()
	u, _ := url.Parse(srv.URL)
	// Deliberately no DATABASE_URL: the probe must not need configuration.
	setEnv(t, "PORT", u.Port())

	if code, _, _ := runCapture("healthcheck"); code != 0 {
		t.Errorf("healthy: exit %d", code)
	}
	status = http.StatusServiceUnavailable
	if code, _, stderr := runCapture("healthcheck"); code != 1 || stderr == "" {
		t.Errorf("unhealthy: exit %d stderr %q", code, stderr)
	}
}

func TestRun_MigrateAppliesTheSchema(t *testing.T) {
	databaseURL := testdb.URL(t)
	setEnv(t, "DATABASE_URL", databaseURL, "APP_URL", "http://localhost:8080", "ALLOW_INSECURE_TRANSPORT", "1")

	if code, stdout, stderr := runCapture("migrate"); code != 0 {
		t.Fatalf("exit %d\nstdout %s\nstderr %s", code, stdout, stderr)
	}
	conn, err := pgx.Connect(context.Background(), databaseURL)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = conn.Close(context.Background()) }()
	var present bool
	if err := conn.QueryRow(context.Background(), "SELECT to_regclass('platform.rate_limit') IS NOT NULL").Scan(&present); err != nil || !present {
		t.Errorf("platform.rate_limit present = %v (%v)", present, err)
	}
}

// startServe runs serve on a loopback listener and returns its base URL and a
// func that stops it and returns the exit code.
func startServe(t *testing.T, withAPI bool) (string, func() int) {
	t.Helper()
	_, databaseURL := testdb.Migrated(t)
	cfg, err := config.Load(map[string]string{
		"DATABASE_URL":             databaseURL,
		"APP_URL":                  "http://localhost:8080",
		"ALLOW_INSECURE_TRANSPORT": "1",
		"SHUTDOWN_TIMEOUT":         "5s",
	})
	if err != nil {
		t.Fatal(err)
	}
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan int, 1)
	go func() { done <- serve(ctx, slog.New(slog.DiscardHandler), cfg, ln, withAPI) }()

	base := "http://" + ln.Addr().String()
	waitReady(t, base)
	return base, func() int {
		cancel()
		select {
		case code := <-done:
			return code
		case <-time.After(10 * time.Second):
			t.Fatal("serve did not return after cancellation")
			return -1
		}
	}
}

func waitReady(t *testing.T, base string) {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		if resp, err := http.Get(base + "/health/ready"); err == nil {
			_ = resp.Body.Close()
			if resp.StatusCode == http.StatusOK {
				return
			}
		}
		time.Sleep(50 * time.Millisecond)
	}
	t.Fatalf("%s never became ready", base)
}

func body(t *testing.T, target string) (int, string, http.Header) {
	t.Helper()
	resp, err := http.Get(target)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = resp.Body.Close() }()
	b, _ := io.ReadAll(resp.Body)
	return resp.StatusCode, string(b), resp.Header
}

func TestServe_ServesTheWholeStackAndDrainsOnCancel(t *testing.T) {
	base, stop := startServe(t, true)

	if code, html, hdr := body(t, base+"/"); code != http.StatusOK || !strings.Contains(html, "window.__VANTIGO_APP__") || hdr.Get("Content-Security-Policy") == "" {
		t.Errorf("SPA: %d, CSP %q, body %q", code, hdr.Get("Content-Security-Policy"), html)
	}
	if code, _, hdr := body(t, base+"/api/v1/nope"); code != http.StatusNotFound || hdr.Get("Content-Type") != "application/problem+json" {
		t.Errorf("API catch-all: %d %q", code, hdr.Get("Content-Type"))
	}
	if code, ready, _ := body(t, base+"/health/ready"); code != http.StatusOK || !strings.Contains(ready, `"version":"dev"`) {
		t.Errorf("ready: %d %q", code, ready)
	}
	if code := stop(); code != 0 {
		t.Errorf("exit %d after a clean shutdown", code)
	}
}

func TestServe_WorkerServesOnlyHealth(t *testing.T) {
	base, stop := startServe(t, false)
	defer stop()
	if code, _, _ := body(t, base+"/"); code != http.StatusNotFound {
		t.Errorf("worker served / with %d", code)
	}
}

func TestServeUntilDone_LetsInFlightRequestsFinish(t *testing.T) {
	entered, release := make(chan struct{}), make(chan struct{})
	srv := &http.Server{Handler: http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		close(entered)
		<-release
		_, _ = w.Write([]byte("finished"))
	})}
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan int, 1)
	go func() { done <- serveUntilDone(ctx, slog.New(slog.DiscardHandler), srv, ln, 5*time.Second) }()

	result := make(chan string, 1)
	go func() {
		resp, err := http.Get("http://" + ln.Addr().String())
		if err != nil {
			result <- "error: " + err.Error()
			return
		}
		defer func() { _ = resp.Body.Close() }()
		b, _ := io.ReadAll(resp.Body)
		result <- string(b)
	}()

	<-entered
	cancel() // the shutdown signal arrives mid-request
	select {
	case code := <-done:
		t.Fatalf("returned %d before the in-flight request finished", code)
	case <-time.After(100 * time.Millisecond):
	}
	close(release)

	if got := <-result; got != "finished" {
		t.Errorf("in-flight request got %q", got)
	}
	if code := <-done; code != 0 {
		t.Errorf("exit %d, want 0", code)
	}
}
```

- [ ] **Step 2: Run the tests to verify they fail**

Run: `cd apps/server && go test ./cmd/vantigo/`
Expected: FAIL to compile with `undefined: run`, `undefined: parseArgs`, `undefined: serve`.

- [ ] **Step 3: Implement the command**

Create `apps/server/cmd/vantigo/main.go`:

```go
// Command vantigo is the container's entrypoint and the application's only
// composition root: the one place that reads the environment, opens the pool
// and assembles every collaborator. Nothing below it reads os.Getenv.
//
// It is also the dispatch table — one image, several commands:
//
//	api          migrate under the advisory lock, then serve SPA + API + health
//	server       serve only; never migrates (what replicas run)
//	worker       background work plus health (health only until workers exist)
//	migrate      apply migrations and exit 0/1
//	seed         development data (APP_ENV=development only)
//	healthcheck  probe this container's /health/ready and exit 0/1
package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	// The distroless image has no zoneinfo; embed it so time.LoadLocation
	// works for every zone, not just UTC.
	_ "time/tzdata"

	"github.com/vantigo-io/vantigo/server/internal/buildinfo"
	"github.com/vantigo-io/vantigo/server/internal/config"
	"github.com/vantigo-io/vantigo/server/internal/db"
	"github.com/vantigo-io/vantigo/server/internal/health"
	"github.com/vantigo-io/vantigo/server/internal/server"
	"github.com/vantigo-io/vantigo/server/internal/web"
)

const usage = "usage: vantigo <api|server|worker|migrate|seed|healthcheck>"

type mode int

const (
	modeUsage mode = iota
	modeAPI
	modeServer
	modeWorker
	modeMigrate
	modeSeed
	modeHealthcheck
)

// readHeaderTimeout is the slowloris brake. There is deliberately no
// whole-request read or write timeout: attachment uploads and downloads
// stream, and the overall budget belongs to the proxy in front.
const (
	readHeaderTimeout = 15 * time.Second
	idleTimeout       = 120 * time.Second
)

func main() { os.Exit(run(os.Args[1:], os.Stdout, os.Stderr)) }

// parseArgs reads nothing but its arguments, which is what makes the
// dispatch table testable. It returns the raw command for error messages.
func parseArgs(args []string) (mode, string) {
	if len(args) == 0 {
		return modeUsage, ""
	}
	switch args[0] {
	case "api":
		return modeAPI, args[0]
	case "server":
		return modeServer, args[0]
	case "worker":
		return modeWorker, args[0]
	case "migrate":
		return modeMigrate, args[0]
	case "seed":
		return modeSeed, args[0]
	case "healthcheck":
		return modeHealthcheck, args[0]
	default:
		return modeUsage, args[0]
	}
}

// run is main's testable body: it returns the exit code instead of exiting.
func run(args []string, stdout, stderr io.Writer) int {
	m, raw := parseArgs(args)
	switch m {
	case modeUsage:
		// An unknown command must fail loudly: a typo in a Kubernetes Job must
		// not quietly become a web server that never completes.
		if raw != "" {
			_, _ = fmt.Fprintf(stderr, "unknown command %q\n", raw)
		}
		_, _ = fmt.Fprintln(stderr, usage)
		return 2
	case modeHealthcheck:
		// Constructs nothing: a liveness probe must not fail because
		// DATABASE_URL is wrong — that is /health/ready's job to report.
		return healthcheck(os.Getenv("PORT"), stderr)
	}

	cfg, err := config.FromOS()
	if err != nil {
		_, _ = fmt.Fprintln(stderr, err)
		return 1
	}
	logger := slog.New(slog.NewJSONHandler(stdout, &slog.HandlerOptions{Level: cfg.LogLevel}))
	slog.SetDefault(logger)

	switch m {
	case modeMigrate:
		return migrate(logger, cfg)
	case modeSeed:
		if !cfg.IsDevelopment() {
			_, _ = fmt.Fprintln(stderr, "the seed command is only available with APP_ENV=development")
			return 2
		}
		logger.Info("nothing to seed yet: no modules are installed")
		return 0
	case modeAPI:
		if code := migrate(logger, cfg); code != 0 {
			return code
		}
	}

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGTERM, syscall.SIGINT)
	defer stop()

	ln, err := net.Listen("tcp", fmt.Sprintf(":%d", cfg.Port))
	if err != nil {
		logger.Error("cannot listen", "port", cfg.Port, "error", err)
		return 1
	}
	return serve(ctx, logger, cfg, ln, m != modeWorker)
}

func healthcheck(port string, stderr io.Writer) int {
	if port == "" {
		port = "8080"
	}
	if err := health.Probe(context.Background(), "http://127.0.0.1:"+port); err != nil {
		_, _ = fmt.Fprintln(stderr, err)
		return 1
	}
	return 0
}

// migrate runs on context.Background: aborting DDL halfway on SIGTERM is
// worse than being killed after the grace period.
func migrate(logger *slog.Logger, cfg *config.Config) int {
	if err := db.ApplyMigrations(context.Background(), cfg.MigrationsDatabaseURL); err != nil {
		logger.Error("migration failed", "error", err)
		return 1
	}
	return 0
}

// serve builds the handler for this command and runs it on ln until ctx is
// cancelled. withAPI=false is worker mode: health only.
func serve(ctx context.Context, logger *slog.Logger, cfg *config.Config, ln net.Listener, withAPI bool) int {
	defer func() { _ = ln.Close() }()

	// Startup is not cancelled by the shutdown signal: a SIGTERM during boot
	// should produce a process that came up and then drained, not a half-built one.
	pool, err := db.Open(context.WithoutCancel(ctx), cfg.DatabaseURL)
	if err != nil {
		logger.Error("startup failed", "error", err)
		return 1
	}
	defer pool.Close()

	healthHandler := health.Handler(logger, buildinfo.Version, health.Check{Name: "postgres", Run: pool.Ping})
	handler := healthHandler
	command := "worker"
	if withAPI {
		command = "api"
		assets := web.Assets()
		index, err := web.NewIndex(assets, cfg.BasePath, cfg.Branding)
		if err != nil {
			logger.Error("startup failed", "error", err)
			return 1
		}
		handler = server.New(server.Options{
			Config: cfg,
			Logger: logger,
			Index:  index,
			Assets: assets,
			Health: healthHandler,
		})
	}

	srv := &http.Server{
		Handler:           handler,
		ReadHeaderTimeout: readHeaderTimeout,
		IdleTimeout:       idleTimeout,
		ErrorLog:          slog.NewLogLogger(logger.Handler(), slog.LevelWarn),
	}
	logger.Info("vantigo is listening",
		"version", buildinfo.Version, "command", command, "addr", ln.Addr().String(),
		"app_url", cfg.AppOrigin, "base_path", cfg.BasePath, "env", cfg.Env)
	if cfg.AllowInsecureTransport {
		logger.Warn("ALLOW_INSECURE_TRANSPORT=1: plaintext HTTP, database and SMTP transport are accepted; local and evaluation use only")
	}
	return serveUntilDone(ctx, logger, srv, ln, cfg.ShutdownTimeout)
}

// serveUntilDone serves until ctx is cancelled, then drains in-flight
// requests for up to timeout. The orchestrator's termination grace period
// must exceed it.
func serveUntilDone(ctx context.Context, logger *slog.Logger, srv *http.Server, ln net.Listener, timeout time.Duration) int {
	errc := make(chan error, 1)
	go func() { errc <- srv.Serve(ln) }()

	select {
	case err := <-errc:
		if !errors.Is(err, http.ErrServerClosed) {
			logger.Error("server stopped unexpectedly", "error", err)
		}
		return 1
	case <-ctx.Done():
	}

	logger.Info("shutdown signal received; draining", "timeout", timeout)
	shutdownCtx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()
	if err := srv.Shutdown(shutdownCtx); err != nil {
		logger.Error("in-flight requests did not finish in time", "error", err)
		return 1
	}
	logger.Info("shutdown complete")
	return 0
}
```

- [ ] **Step 4: Run the tests to verify they pass**

Run: `cd apps/server && go mod tidy && go test ./... -count=1 && golangci-lint run`
Expected: every package PASS, including `cmd/vantigo`; `0 issues.`

- [ ] **Step 5: Run the binary by hand**

Run (the test PostgreSQL from Task 6 must be up):

```bash
cd apps/server
DATABASE_URL=postgres://vantigo:vantigo@127.0.0.1:55432/vantigo_test?sslmode=disable \
APP_URL=http://localhost:8080 APP_ENV=development go run ./cmd/vantigo api &
sleep 3
curl -s localhost:8080/health/ready; echo
curl -s localhost:8080/ | grep -o '<title>.*</title>'
curl -s -o /dev/null -w '%{http_code} %{content_type}\n' localhost:8080/api/v1/nope
kill %1; wait
```

Expected: `{"status":"healthy","version":"dev"}`, then `<title>Vantigo</title>` (the placeholder), then `404 application/problem+json`, and a `shutdown complete` log line after `kill`. This migrates the shared `vantigo_test` database, which is harmless: tests use their own databases.

- [ ] **Step 6: Commit**

```bash
git add apps/server/go.mod apps/server/go.sum apps/server/cmd
git commit -m "feat(server): add the vantigo command

The composition root and dispatch table (api, server, worker, migrate,
seed, healthcheck), migrations before listening, and a graceful drain on
SIGTERM bounded by SHUTDOWN_TIMEOUT."
```

---

### Task 11: OpenTelemetry — traces, metrics and logs when configured

**Files:**
- Create: `apps/server/internal/telemetry/telemetry.go`, `apps/server/internal/telemetry/logger.go`, `apps/server/internal/telemetry/http.go`
- Test: `apps/server/internal/telemetry/telemetry_test.go`
- Modify: `apps/server/cmd/vantigo/main.go` (logger construction, `Setup`, handler wrapping)
- Modify: `apps/server/internal/db/db.go` (query tracing), `apps/server/internal/db/db_test.go` (new test)

**Interfaces:**
- Consumes: `httpx.TraceID` (Task 3), `run`/`serve` (Task 10).
- Produces:
  - `type Options struct { Version, Environment string }`, `type Signals struct { Traces, Metrics, Logs bool }`
  - `func Setup(ctx context.Context, o Options) (Signals, func(context.Context) error, error)`
  - `func NewLogger(w io.Writer, level slog.Level, exportLogs bool) *slog.Logger`
  - `func HTTPHandler(h http.Handler, basePath string) http.Handler`, `func IsAPIPath(path, basePath string) bool`

Each signal gets an OTLP http/protobuf exporter only when `OTEL_EXPORTER_OTLP_ENDPOINT` or `OTEL_EXPORTER_OTLP_<SIGNAL>_ENDPOINT` is set, as the .NET host did. `OTEL_SDK_DISABLED=true` turns everything off. gRPC isn't supported, and asking for it is a startup error rather than a silent no-op. Only API requests are traced. The resource is `service.name=vantigo`, `service.version=<buildinfo.Version>`, `deployment.environment.name=<APP_ENV>`, plus anything in `OTEL_RESOURCE_ATTRIBUTES`.

- [ ] **Step 1: Write the failing tests**

Create `apps/server/internal/telemetry/telemetry_test.go`:

```go
package telemetry

import (
	"bytes"
	"context"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"sync"
	"testing"

	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/log/global"
	lognoop "go.opentelemetry.io/otel/log/noop"
	metricnoop "go.opentelemetry.io/otel/metric/noop"
	"go.opentelemetry.io/otel/propagation"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/sdk/trace/tracetest"
	tracenoop "go.opentelemetry.io/otel/trace/noop"

	"github.com/vantigo-io/vantigo/server/internal/httpx"
)

// isolate blanks every OTEL_* variable Setup reads and restores the global
// providers afterwards, so tests cannot leak into each other.
func isolate(t *testing.T) {
	t.Helper()
	for _, k := range []string{"OTEL_SDK_DISABLED", "OTEL_EXPORTER_OTLP_ENDPOINT", "OTEL_EXPORTER_OTLP_PROTOCOL"} {
		t.Setenv(k, "")
	}
	for _, s := range []string{"TRACES", "METRICS", "LOGS"} {
		t.Setenv("OTEL_EXPORTER_OTLP_"+s+"_ENDPOINT", "")
		t.Setenv("OTEL_EXPORTER_OTLP_"+s+"_PROTOCOL", "")
	}
	t.Cleanup(func() {
		otel.SetTracerProvider(tracenoop.NewTracerProvider())
		otel.SetMeterProvider(metricnoop.NewMeterProvider())
		global.SetLoggerProvider(lognoop.NewLoggerProvider())
		otel.SetTextMapPropagator(propagation.NewCompositeTextMapPropagator())
	})
}

// collector is a fake OTLP/HTTP endpoint that records which paths were posted to.
type collector struct {
	*httptest.Server
	mu    sync.Mutex
	paths []string
}

func newCollector(t *testing.T) *collector {
	c := &collector{}
	c.Server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.Copy(io.Discard, r.Body)
		c.mu.Lock()
		c.paths = append(c.paths, r.URL.Path)
		c.mu.Unlock()
		w.WriteHeader(http.StatusOK)
	}))
	t.Cleanup(c.Close)
	return c
}

func (c *collector) received(path string) bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	return slices.Contains(c.paths, path)
}

func TestSetup_NothingConfiguredInstallsNothing(t *testing.T) {
	isolate(t)
	signals, shutdown, err := Setup(context.Background(), Options{Version: "test"})
	if err != nil {
		t.Fatal(err)
	}
	if signals != (Signals{}) {
		t.Errorf("signals = %+v, want none", signals)
	}
	if err := shutdown(context.Background()); err != nil {
		t.Errorf("shutdown: %v", err)
	}
}

func TestSetup_SDKDisabledWins(t *testing.T) {
	isolate(t)
	t.Setenv("OTEL_SDK_DISABLED", "true")
	t.Setenv("OTEL_EXPORTER_OTLP_ENDPOINT", "http://127.0.0.1:4318")
	if signals, _, err := Setup(context.Background(), Options{}); err != nil || signals != (Signals{}) {
		t.Errorf("signals = %+v err = %v, want none", signals, err)
	}
}

func TestSetup_RejectsProtocolsOtherThanHTTPProtobuf(t *testing.T) {
	isolate(t)
	t.Setenv("OTEL_EXPORTER_OTLP_ENDPOINT", "http://127.0.0.1:4317")
	t.Setenv("OTEL_EXPORTER_OTLP_TRACES_PROTOCOL", "grpc")
	if _, _, err := Setup(context.Background(), Options{}); err == nil || !strings.Contains(err.Error(), "OTEL_EXPORTER_OTLP_TRACES_PROTOCOL") {
		t.Errorf("err = %v, want a rejection naming the variable", err)
	}
}

func TestSetup_ExportsEveryConfiguredSignal(t *testing.T) {
	isolate(t)
	c := newCollector(t)
	t.Setenv("OTEL_EXPORTER_OTLP_ENDPOINT", c.URL)

	ctx := context.Background()
	signals, shutdown, err := Setup(ctx, Options{Version: "1.2.3", Environment: "production"})
	if err != nil {
		t.Fatal(err)
	}
	if signals != (Signals{Traces: true, Metrics: true, Logs: true}) {
		t.Fatalf("signals = %+v, want all three", signals)
	}

	_, span := otel.Tracer("test").Start(ctx, "work")
	span.End()
	counter, err := otel.Meter("test").Int64Counter("test.hits")
	if err != nil {
		t.Fatal(err)
	}
	counter.Add(ctx, 1)
	NewLogger(io.Discard, slog.LevelInfo, true).Info("hello")

	if err := shutdown(ctx); err != nil { // flushes every signal
		t.Fatalf("shutdown: %v", err)
	}
	for _, path := range []string{"/v1/traces", "/v1/metrics", "/v1/logs"} {
		if !c.received(path) {
			t.Errorf("nothing was exported to %s (got %v)", path, c.paths)
		}
	}
}

func TestSetup_OnlyTheSignalWithAnEndpointIsExported(t *testing.T) {
	isolate(t)
	c := newCollector(t)
	t.Setenv("OTEL_EXPORTER_OTLP_TRACES_ENDPOINT", c.URL+"/v1/traces")

	signals, shutdown, err := Setup(context.Background(), Options{})
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = shutdown(context.Background()) }()
	if signals != (Signals{Traces: true}) {
		t.Errorf("signals = %+v, want traces only", signals)
	}
}

func TestIsAPIPath(t *testing.T) {
	for _, tc := range []struct {
		path, base string
		want       bool
	}{
		{"/api", "", true},
		{"/api/v1/customers", "", true},
		{"/vantigo/api/v1/customers", "/vantigo", true},
		{"/health/ready", "", false},
		{"/", "", false},
		{"/customers/42", "", false},
		{"/apiary", "", false},
		{"/assets/index-abc.js", "", false},
	} {
		if got := IsAPIPath(tc.path, tc.base); got != tc.want {
			t.Errorf("IsAPIPath(%q, %q) = %v, want %v", tc.path, tc.base, got, tc.want)
		}
	}
}

func TestHTTPHandler_TracesAPIRequestsOnlyAndExposesTheTraceID(t *testing.T) {
	isolate(t)
	recorder := tracetest.NewSpanRecorder()
	otel.SetTracerProvider(sdktrace.NewTracerProvider(sdktrace.WithSpanProcessor(recorder)))

	var seen string
	h := HTTPHandler(http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) {
		if IsAPIPath(r.URL.Path, "/vantigo") {
			seen = httpx.TraceID(r)
		}
	}), "/vantigo")
	for _, path := range []string{"/vantigo/api/v1/ping", "/health/ready", "/vantigo/customers"} {
		h.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest(http.MethodGet, path, nil))
	}

	spans := recorder.Ended()
	if len(spans) != 1 {
		t.Fatalf("%d spans, want 1 (API requests only)", len(spans))
	}
	if want := spans[0].SpanContext().TraceID().String(); seen != want {
		t.Errorf("handler saw trace id %q, span has %q", seen, want)
	}
}

func TestNewLogger_WritesJSONAtTheConfiguredLevel(t *testing.T) {
	var buf bytes.Buffer
	logger := NewLogger(&buf, slog.LevelInfo, false)
	logger.Debug("hidden")
	logger.Info("shown", "k", "v")

	if strings.Contains(buf.String(), "hidden") || !strings.Contains(buf.String(), `"msg":"shown"`) || !strings.Contains(buf.String(), `"k":"v"`) {
		t.Errorf("log output = %s", buf.String())
	}
}

func TestFanout_SendsEachRecordToEveryHandlerAboveTheLevel(t *testing.T) {
	var a, b bytes.Buffer
	logger := slog.New(fanout{min: slog.LevelInfo, handlers: []slog.Handler{
		slog.NewJSONHandler(&a, &slog.HandlerOptions{Level: slog.LevelDebug}),
		slog.NewJSONHandler(&b, &slog.HandlerOptions{Level: slog.LevelDebug}),
	}}).With("service", "vantigo")
	logger.Debug("dropped")
	logger.Info("kept")

	for _, out := range []string{a.String(), b.String()} {
		if strings.Contains(out, "dropped") || !strings.Contains(out, `"msg":"kept"`) || !strings.Contains(out, `"service":"vantigo"`) {
			t.Errorf("handler output = %s", out)
		}
	}
}
```

Append to `apps/server/internal/db/db_test.go`, and add these imports to its import block: `"go.opentelemetry.io/otel"`, `sdktrace "go.opentelemetry.io/otel/sdk/trace"`, `"go.opentelemetry.io/otel/sdk/trace/tracetest"`, `tracenoop "go.opentelemetry.io/otel/trace/noop"`:

```go
func TestOpen_TracesQueries(t *testing.T) {
	recorder := tracetest.NewSpanRecorder()
	otel.SetTracerProvider(sdktrace.NewTracerProvider(sdktrace.WithSpanProcessor(recorder)))
	defer otel.SetTracerProvider(tracenoop.NewTracerProvider())

	pool, err := db.Open(context.Background(), testdb.URL(t))
	if err != nil {
		t.Fatal(err)
	}
	defer pool.Close()
	if _, err := pool.Exec(context.Background(), "SELECT 1"); err != nil {
		t.Fatal(err)
	}
	for _, s := range recorder.Ended() {
		if strings.Contains(s.Name(), "query") {
			return
		}
	}
	t.Errorf("no query span among %d spans", len(recorder.Ended()))
}
```

- [ ] **Step 2: Run the tests to verify they fail**

Run: `cd apps/server && go test ./internal/telemetry/ ./internal/db/`
Expected: FAIL. The telemetry package doesn't compile (`undefined: Setup`, and the OTel modules aren't in `go.mod` yet: `no required module provides package go.opentelemetry.io/otel/sdk/trace`).

- [ ] **Step 3: Add the dependencies**

Run:

```bash
cd apps/server && go get \
  go.opentelemetry.io/otel@latest \
  go.opentelemetry.io/otel/sdk@latest \
  go.opentelemetry.io/otel/sdk/metric@latest \
  go.opentelemetry.io/otel/sdk/log@latest \
  go.opentelemetry.io/otel/log@latest \
  go.opentelemetry.io/otel/exporters/otlp/otlptrace/otlptracehttp@latest \
  go.opentelemetry.io/otel/exporters/otlp/otlpmetric/otlpmetrichttp@latest \
  go.opentelemetry.io/otel/exporters/otlp/otlplog/otlploghttp@latest \
  go.opentelemetry.io/contrib/bridges/otelslog@latest \
  go.opentelemetry.io/contrib/instrumentation/net/http/otelhttp@latest \
  go.opentelemetry.io/contrib/instrumentation/runtime@latest \
  github.com/exaring/otelpgx@latest
```

`@latest` resolves a mutually compatible set of the OTel modules, and `go.sum` pins exactly what was resolved.

- [ ] **Step 4: Implement telemetry**

Create `apps/server/internal/telemetry/telemetry.go`:

```go
// Package telemetry wires OpenTelemetry: one OTLP/HTTP exporter per signal
// that has an endpoint configured, the slog bridge for logs, and HTTP server
// instrumentation. The SDK reads the standard OTEL_* variables itself, which
// is the one exception to "only cmd/vantigo reads the environment".
package telemetry

import (
	"context"
	"errors"
	"fmt"
	"os"

	otelruntime "go.opentelemetry.io/contrib/instrumentation/runtime"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/exporters/otlp/otlplog/otlploghttp"
	"go.opentelemetry.io/otel/exporters/otlp/otlpmetric/otlpmetrichttp"
	"go.opentelemetry.io/otel/exporters/otlp/otlptrace/otlptracehttp"
	"go.opentelemetry.io/otel/log/global"
	"go.opentelemetry.io/otel/propagation"
	sdklog "go.opentelemetry.io/otel/sdk/log"
	sdkmetric "go.opentelemetry.io/otel/sdk/metric"
	"go.opentelemetry.io/otel/sdk/resource"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
)

// Options describe the process for the telemetry resource.
type Options struct {
	Version     string
	Environment string
}

// Signals reports which signals are being exported.
type Signals struct {
	Traces, Metrics, Logs bool
}

// Setup installs a global provider for every signal with an OTLP endpoint and
// returns a shutdown func that flushes them. With nothing configured it
// installs nothing: the global no-op providers stay, and so does the cost of
// using them — none.
func Setup(ctx context.Context, o Options) (Signals, func(context.Context) error, error) {
	var signals Signals
	var shutdowns []func(context.Context) error
	shutdown := func(ctx context.Context) error {
		var errs []error
		for i := len(shutdowns) - 1; i >= 0; i-- {
			errs = append(errs, shutdowns[i](ctx))
		}
		return errors.Join(errs...)
	}
	fail := func(err error) (Signals, func(context.Context) error, error) {
		_ = shutdown(ctx)
		return Signals{}, func(context.Context) error { return nil }, err
	}

	if os.Getenv("OTEL_SDK_DISABLED") == "true" {
		return signals, shutdown, nil
	}
	for _, name := range []string{"OTEL_EXPORTER_OTLP_PROTOCOL", "OTEL_EXPORTER_OTLP_TRACES_PROTOCOL",
		"OTEL_EXPORTER_OTLP_METRICS_PROTOCOL", "OTEL_EXPORTER_OTLP_LOGS_PROTOCOL"} {
		if p := os.Getenv(name); p != "" && p != "http/protobuf" {
			return fail(fmt.Errorf("telemetry: %s=%q is not supported; use http/protobuf", name, p))
		}
	}

	res, err := resource.Merge(resource.Default(), resource.NewSchemaless(
		attribute.String("service.name", "vantigo"),
		attribute.String("service.version", o.Version),
		attribute.String("deployment.environment.name", o.Environment),
	))
	if err != nil {
		return fail(fmt.Errorf("telemetry: resource: %w", err))
	}

	if exporting("TRACES") {
		exp, err := otlptracehttp.New(ctx)
		if err != nil {
			return fail(fmt.Errorf("telemetry: trace exporter: %w", err))
		}
		tp := sdktrace.NewTracerProvider(sdktrace.WithBatcher(exp), sdktrace.WithResource(res))
		shutdowns = append(shutdowns, tp.Shutdown)
		otel.SetTracerProvider(tp)
		otel.SetTextMapPropagator(propagation.NewCompositeTextMapPropagator(propagation.TraceContext{}, propagation.Baggage{}))
		signals.Traces = true
	}
	if exporting("METRICS") {
		exp, err := otlpmetrichttp.New(ctx)
		if err != nil {
			return fail(fmt.Errorf("telemetry: metric exporter: %w", err))
		}
		mp := sdkmetric.NewMeterProvider(sdkmetric.WithReader(sdkmetric.NewPeriodicReader(exp)), sdkmetric.WithResource(res))
		shutdowns = append(shutdowns, mp.Shutdown)
		otel.SetMeterProvider(mp)
		if err := otelruntime.Start(otelruntime.WithMeterProvider(mp)); err != nil {
			return fail(fmt.Errorf("telemetry: runtime metrics: %w", err))
		}
		signals.Metrics = true
	}
	if exporting("LOGS") {
		exp, err := otlploghttp.New(ctx)
		if err != nil {
			return fail(fmt.Errorf("telemetry: log exporter: %w", err))
		}
		lp := sdklog.NewLoggerProvider(sdklog.WithProcessor(sdklog.NewBatchProcessor(exp)), sdklog.WithResource(res))
		shutdowns = append(shutdowns, lp.Shutdown)
		global.SetLoggerProvider(lp)
		signals.Logs = true
	}
	return signals, shutdown, nil
}

// exporting reports whether signal (TRACES, METRICS, LOGS) has an endpoint.
func exporting(signal string) bool {
	return os.Getenv("OTEL_EXPORTER_OTLP_ENDPOINT") != "" || os.Getenv("OTEL_EXPORTER_OTLP_"+signal+"_ENDPOINT") != ""
}
```

Create `apps/server/internal/telemetry/logger.go`:

```go
package telemetry

import (
	"context"
	"errors"
	"io"
	"log/slog"

	"go.opentelemetry.io/contrib/bridges/otelslog"
)

// NewLogger returns the process logger: JSON lines to w at level and, when
// the logs signal is exported, the same records to OpenTelemetry through the
// slog bridge (which uses the global LoggerProvider Setup installed).
func NewLogger(w io.Writer, level slog.Level, exportLogs bool) *slog.Logger {
	var h slog.Handler = slog.NewJSONHandler(w, &slog.HandlerOptions{Level: level})
	if exportLogs {
		h = fanout{min: level, handlers: []slog.Handler{h, otelslog.NewHandler("vantigo")}}
	}
	return slog.New(h)
}

// fanout sends every record at or above min to each of its handlers.
type fanout struct {
	min      slog.Level
	handlers []slog.Handler
}

func (f fanout) Enabled(_ context.Context, level slog.Level) bool { return level >= f.min }

func (f fanout) Handle(ctx context.Context, r slog.Record) error {
	var errs []error
	for _, h := range f.handlers {
		if h.Enabled(ctx, r.Level) {
			errs = append(errs, h.Handle(ctx, r.Clone()))
		}
	}
	return errors.Join(errs...)
}

func (f fanout) WithAttrs(attrs []slog.Attr) slog.Handler {
	hs := make([]slog.Handler, len(f.handlers))
	for i, h := range f.handlers {
		hs[i] = h.WithAttrs(attrs)
	}
	return fanout{min: f.min, handlers: hs}
}

func (f fanout) WithGroup(name string) slog.Handler {
	hs := make([]slog.Handler, len(f.handlers))
	for i, h := range f.handlers {
		hs[i] = h.WithGroup(name)
	}
	return fanout{min: f.min, handlers: hs}
}
```

Create `apps/server/internal/telemetry/http.go`:

```go
package telemetry

import (
	"net/http"
	"strings"

	"go.opentelemetry.io/contrib/instrumentation/net/http/otelhttp"
)

// HTTPHandler wraps the process handler in a server span per API request.
// It must be the outermost layer, so httpx.TraceID — and with it every
// problem response and request-log line — carries the span's trace id. Health
// probes and SPA/static requests are not traced: at probe frequency they would
// drown the useful spans.
func HTTPHandler(h http.Handler, basePath string) http.Handler {
	return otelhttp.NewHandler(h, "http.server", otelhttp.WithFilter(func(r *http.Request) bool {
		return IsAPIPath(r.URL.Path, basePath)
	}))
}

// IsAPIPath reports whether path, as received (still carrying basePath), is
// in the /api namespace.
func IsAPIPath(path, basePath string) bool {
	p := strings.TrimPrefix(path, basePath)
	return p == "/api" || strings.HasPrefix(p, "/api/")
}
```

- [ ] **Step 5: Trace database queries**

In `apps/server/internal/db/db.go`, add the import `"github.com/exaring/otelpgx"` and set the tracer between `ParseConfig` and `NewWithConfig`:

```go
	cfg, err := pgxpool.ParseConfig(databaseURL)
	if err != nil {
		return nil, fmt.Errorf("db: parse connection string: %w", err)
	}
	// Query spans join the request's trace. With no tracer provider installed
	// they go to the global no-op and cost next to nothing.
	cfg.ConnConfig.Tracer = otelpgx.NewTracer()
	pool, err := pgxpool.NewWithConfig(ctx, cfg)
```

- [ ] **Step 6: Wire telemetry into the command**

In `apps/server/cmd/vantigo/main.go`:

1. Add the import `"github.com/vantigo-io/vantigo/server/internal/telemetry"`.
2. Replace the two logger lines in `run`:

```go
	logger := slog.New(slog.NewJSONHandler(stdout, &slog.HandlerOptions{Level: cfg.LogLevel}))
	slog.SetDefault(logger)
```

with:

```go
	logger := telemetry.NewLogger(stdout, cfg.LogLevel, false)
	slog.SetDefault(logger)
```

3. In `run`, directly after `defer stop()` and before `net.Listen`, add:

```go
	signals, shutdownTelemetry, err := telemetry.Setup(ctx, telemetry.Options{Version: buildinfo.Version, Environment: string(cfg.Env)})
	if err != nil {
		logger.Error("telemetry setup failed", "error", err)
		return 1
	}
	defer func() {
		flushCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		if err := shutdownTelemetry(flushCtx); err != nil {
			logger.Warn("telemetry did not flush", "error", err)
		}
	}()
	if signals.Logs {
		logger = telemetry.NewLogger(stdout, cfg.LogLevel, true)
		slog.SetDefault(logger)
	}
	logger.Info("telemetry", "traces", signals.Traces, "metrics", signals.Metrics, "logs", signals.Logs)
```

4. Add `"github.com/vantigo-io/vantigo/server/internal/buildinfo"` to the imports if the linter reports it missing. It's already imported by `serve`.
5. In `serve`, wrap the API handler so the span is outermost:

```go
		handler = telemetry.HTTPHandler(server.New(server.Options{
			Config: cfg,
			Logger: logger,
			Index:  index,
			Assets: assets,
			Health: healthHandler,
		}), cfg.BasePath)
```

- [ ] **Step 7: Run the tests to verify they pass**

Run: `cd apps/server && go mod tidy && go test ./... -count=1 && golangci-lint run`
Expected: every package PASS, including the new `telemetry` tests and `TestOpen_TracesQueries`; `0 issues.`

- [ ] **Step 8: Commit**

```bash
git add apps/server/go.mod apps/server/go.sum apps/server/internal/telemetry apps/server/internal/db apps/server/cmd
git commit -m "feat(server): export traces, metrics and logs over OTLP when configured

Each signal gets an OTLP/HTTP exporter only when its endpoint is set, API
requests and database queries are traced, runtime metrics are recorded,
and slog records fan out to the OTel log bridge."
```

---

### Task 12: Native artifacts, the COPY-only image and the smoke test

**Files:**
- Modify: `.gitignore` (append `/dist/`)
- Create: `scripts/spa-embed-overlay.sh`, `scripts/restore-embed-overlay.sh`, `scripts/build-artifacts.sh`, `scripts/build-image.sh`, `scripts/smoke-image.sh`
- Create: `Dockerfile`, `.dockerignore`

**Interfaces:**
- Consumes: the `vantigo` binary (Tasks 1–11), the host frontend build (`bun run --cwd apps/host/frontend build` → `apps/host/frontend/dist`).
- Produces: `dist/server/linux/{amd64,arm64}/vantigo`; an image with `ENTRYPOINT ["/app/vantigo"]`, `CMD ["api"]`, user 65532, port 8080, and `HEALTHCHECK` running `vantigo healthcheck`; `scripts/smoke-image.sh <image>` (optional `SMOKE_EXPECT_VERSION`, `SMOKE_APP_PORT`, `SMOKE_WORKER_PORT`); `VANTIGO_VERSION` stamps `buildinfo.Version`.

The test for this task is `scripts/smoke-image.sh` itself: it runs the linked binary inside the shipped image and fails on the first wrong answer.

- [ ] **Step 1: Ignore build output**

Append to `.gitignore`:

```
# Go server build outputs (scripts/build-artifacts.sh, GoReleaser)
/dist/
```

- [ ] **Step 2: Write the embed and build scripts**

Create `scripts/spa-embed-overlay.sh`:

```bash
#!/usr/bin/env bash
# Builds the SPA and overlays it into apps/server/internal/web/dist, where
# go:embed picks it up at compile time. A committed placeholder index.html
# normally sits there so `go build` and `go test` work without a frontend
# build. Callers restore it afterwards (scripts/restore-embed-overlay.sh) so
# the working tree stays clean.
set -euo pipefail
cd "$(dirname "$0")/.."

EMBED_DIR=apps/server/internal/web/dist

echo "==> SPA (vite)"
bun run --cwd apps/host/frontend build

echo "==> embed overlay"
rm -rf "$EMBED_DIR"
mkdir -p "$EMBED_DIR"
cp -R apps/host/frontend/dist/. "$EMBED_DIR/"
```

Create `scripts/restore-embed-overlay.sh`:

```bash
#!/usr/bin/env bash
# Puts the committed placeholder back into the go:embed directory after an
# overlay. Idempotent; a no-op outside a git checkout.
set -euo pipefail
cd "$(dirname "$0")/.."

EMBED_DIR=apps/server/internal/web/dist

if git rev-parse --is-inside-work-tree >/dev/null 2>&1; then
  git clean -qfdx -- "$EMBED_DIR"
  git checkout -q -- "$EMBED_DIR"
fi
```

Create `scripts/build-artifacts.sh`:

```bash
#!/usr/bin/env bash
# Builds everything the container image COPYs, natively — nothing compiles
# inside Docker:
#
#   dist/server/linux/amd64/vantigo
#   dist/server/linux/arm64/vantigo
#
# The SPA is embedded into both binaries (scripts/spa-embed-overlay.sh) and
# the placeholder is restored afterwards, even on failure. The layout mirrors
# GoReleaser's dockers_v2 build context (linux/<arch>/vantigo), so one
# Dockerfile COPY line serves both.
#
# Prerequisites: `mise install` and `bun install --frozen-lockfile`.
# VANTIGO_VERSION stamps the binary (default "dev").
set -euo pipefail
cd "$(dirname "$0")/.."

trap 'bash scripts/restore-embed-overlay.sh' EXIT
bash scripts/spa-embed-overlay.sh

VERSION="${VANTIGO_VERSION:-dev}"
echo "==> server binaries ($VERSION)"
rm -rf dist/server
for arch in amd64 arm64; do
  mkdir -p "dist/server/linux/$arch"
  (cd apps/server && CGO_ENABLED=0 GOOS=linux GOARCH="$arch" go build -trimpath \
    -ldflags="-s -w -X github.com/vantigo-io/vantigo/server/internal/buildinfo.Version=$VERSION" \
    -o "../../dist/server/linux/$arch/vantigo" ./cmd/vantigo)
  echo "    dist/server/linux/$arch/vantigo"
done
ls -lh dist/server/linux/*/
```

Create `scripts/build-image.sh`:

```bash
#!/usr/bin/env bash
# Builds the image for linux/amd64 and linux/arm64 with buildx.
#
#   bash scripts/build-image.sh                                   # verify only
#   TAG=ghcr.io/vantigo-io/vantigo:dev PUSH=1 bash scripts/build-image.sh
#
# Binaries are compiled natively first (scripts/build-artifacts.sh; skipped
# with SKIP_ARTIFACTS=1 when dist/server is already populated). A
# multi-platform result is a manifest list the local image store cannot hold,
# so without PUSH=1 the build is verified and discarded. For single-arch
# iteration use `docker build -t vantigo:dev .` after build-artifacts.sh.
set -euo pipefail
cd "$(dirname "$0")/.."

TAG="${TAG:-vantigo:dev}"
PLATFORMS="${PLATFORMS:-linux/amd64,linux/arm64}"

if [ "${SKIP_ARTIFACTS:-0}" != "1" ] || [ ! -e dist/server/linux/amd64/vantigo ]; then
  bash scripts/build-artifacts.sh
fi

# Only the docker-container driver builds several platforms in one invocation.
BUILDER="${BUILDER:-vantigo}"
if ! docker buildx inspect "$BUILDER" >/dev/null 2>&1; then
  echo "==> creating buildx builder '$BUILDER' (docker-container driver)"
  docker buildx create --name "$BUILDER" --driver docker-container >/dev/null
fi

if [ "${PUSH:-0}" = "1" ]; then
  output=(--push)
  echo "==> building $TAG for $PLATFORMS and pushing"
else
  output=(--output=type=cacheonly)
  echo "==> building $TAG for $PLATFORMS (verify only; PUSH=1 publishes)"
fi

docker buildx build --builder "$BUILDER" --platform "$PLATFORMS" --tag "$TAG" "${output[@]}" .
```

- [ ] **Step 3: Write the image definition**

Create `Dockerfile`:

```dockerfile
# syntax=docker/dockerfile:1

# Vantigo — one image, one static Go binary; the command is argv[1]
# (apps/server/cmd/vantigo is the dispatch table): api (the default here),
# server, worker, migrate, seed, healthcheck.
#
# NOTHING COMPILES IN HERE. Build the binaries natively first:
#
#   bash scripts/build-artifacts.sh   # → dist/server/linux/{amd64,arm64}/vantigo
#
# This file only COPYs the one matching TARGETPLATFORM, so a multi-arch
# buildx build is seconds of copying with no QEMU. The SPA, the migrations and
# the zone database are embedded in the binary.
#
# distroless/static rather than scratch: the same no-shell, no-libc surface,
# plus CA certificates (outbound TLS to PostgreSQL, SMTP and the OIDC
# provider), /tmp and the nonroot user. Pinned by digest; Dependabot bumps it.
FROM gcr.io/distroless/static-debian12:nonroot@sha256:afa5c872c891853ca7fcf1f12c3edb23f7eeef36189728842dd51042ff57f7ab

# buildx sets this per platform ("linux/amd64", "linux/arm64"). BINARY_ROOT
# defaults to the build-artifacts.sh layout; GoReleaser passes ".".
ARG TARGETPLATFORM
ARG BINARY_ROOT=dist/server

COPY ${BINARY_ROOT}/${TARGETPLATFORM}/vantigo /app/vantigo

# Stated explicitly although the :nonroot tag already sets it: CI asserts the
# image's User, and a base-image change must not be able to undo it.
USER 65532:65532

ENV PORT=8080
EXPOSE 8080

# The binary is its own probe client: the image has no shell or curl.
HEALTHCHECK --interval=30s --timeout=5s --start-period=30s --start-interval=2s --retries=3 \
  CMD ["/app/vantigo", "healthcheck"]

ENTRYPOINT ["/app/vantigo"]
CMD ["api"]
```

Create `.dockerignore`:

```
# The image is COPY-only: scripts/build-artifacts.sh builds everything
# natively first. The build context is an allowlist, which also keeps .env
# files and secrets structurally out of reach.
*
!dist/server
```

- [ ] **Step 4: Write the smoke test**

Create `scripts/smoke-image.sh`:

```bash
#!/usr/bin/env bash
# Smoke-tests a built image end to end. `go test` exercises source; this is
# the only check that runs the linked binary inside the shipped image:
# migrations apply from the image, `api` serves the real SPA, health, the API
# catch-all and the security middleware, the container HEALTHCHECK turns
# healthy, and `worker` starts. It needs no fixtures.
#
#   bash scripts/smoke-image.sh vantigo:dev
#   SMOKE_EXPECT_VERSION=0.11.0-pr.7.abc1234 bash scripts/smoke-image.sh vantigo:ci
set -euo pipefail

IMAGE="${1:?usage: scripts/smoke-image.sh <image>}"
APP_PORT="${SMOKE_APP_PORT:-18080}"
WORKER_PORT="${SMOKE_WORKER_PORT:-18081}"
ID="$$"
NET="vantigo-smoke-$ID"
PG="vantigo-smoke-pg-$ID"
APP="vantigo-smoke-app-$ID"
WORKER="vantigo-smoke-worker-$ID"
TMP="$(mktemp -d)"

cleanup() {
  local status=$?
  if [ "$status" -ne 0 ]; then
    for c in "$APP" "$WORKER"; do
      echo "--- logs: $c"
      docker logs "$c" 2>&1 | tail -n 40 || true
    done
  fi
  docker rm -f "$APP" "$WORKER" "$PG" >/dev/null 2>&1 || true
  docker network rm "$NET" >/dev/null 2>&1 || true
  rm -rf "$TMP"
  exit "$status"
}
trap cleanup EXIT

fail() { echo "SMOKE FAIL: $*" >&2; exit 1; }
pass() { echo "ok   $*"; }

wait_for() {
  for _ in $(seq 1 30); do
    curl -fsS -o /dev/null "$1" 2>/dev/null && return 0
    sleep 1
  done
  fail "$1 never answered"
}

# status prints only the HTTP status code of a curl request.
status() { curl -s -o /dev/null -w '%{http_code}' "$@"; }

# --- the image itself --------------------------------------------------------

user="$(docker image inspect --format '{{.Config.User}}' "$IMAGE")"
case "$user" in "" | root | 0 | 0:*) fail "image User is '$user'; it must be non-root" ;; esac
pass "runs as non-root user $user"

set +e
docker run --rm "$IMAGE" bogus >/dev/null 2>&1
code=$?
set -e
[ "$code" -eq 2 ] || fail "an unknown command exited $code, want 2"
pass "an unknown command exits 2"

# --- a database --------------------------------------------------------------

docker network create "$NET" >/dev/null
docker run -d --name "$PG" --network "$NET" \
  -e POSTGRES_USER=vantigo -e POSTGRES_PASSWORD=vantigo -e POSTGRES_DB=vantigo \
  postgres:18-alpine >/dev/null
# -h forces TCP: the unix socket accepts during initdb's first start, before TCP does.
for _ in $(seq 1 30); do
  docker exec "$PG" pg_isready -h 127.0.0.1 -U vantigo -d vantigo >/dev/null 2>&1 && break
  sleep 1
done

env_args=(
  -e "DATABASE_URL=postgres://vantigo:vantigo@$PG:5432/vantigo"
  -e "APP_URL=http://localhost:$APP_PORT"
  -e ALLOW_INSECURE_TRANSPORT=1
)

# --- migrate -------------------------------------------------------------------

docker run --rm --network "$NET" "${env_args[@]}" "$IMAGE" migrate >/dev/null
docker exec "$PG" psql -U vantigo -d vantigo -tAc "SELECT to_regclass('platform.rate_limit') IS NOT NULL" | grep -qx t ||
  fail "migrate did not create platform.rate_limit"
pass "migrate applies the schema"

# --- api -------------------------------------------------------------------------

docker run -d --name "$APP" --network "$NET" -p "127.0.0.1:$APP_PORT:8080" "${env_args[@]}" "$IMAGE" api >/dev/null
base="http://localhost:$APP_PORT"
wait_for "$base/health/live"

curl -fsS "$base/health/ready" >"$TMP/ready" || fail "/health/ready is not 200"
grep -q '"status":"healthy"' "$TMP/ready" || fail "/health/ready: $(cat "$TMP/ready")"
if [ -n "${SMOKE_EXPECT_VERSION:-}" ]; then
  grep -q "\"version\":\"$SMOKE_EXPECT_VERSION\"" "$TMP/ready" || fail "version is not $SMOKE_EXPECT_VERSION: $(cat "$TMP/ready")"
fi
pass "health $(cat "$TMP/ready")"

curl -fsS -D "$TMP/headers" -o "$TMP/index" "$base/customers"
grep -q 'window.__VANTIGO_APP__' "$TMP/index" || fail "the SPA index has no runtime config"
if grep -q 'built without the frontend' "$TMP/index"; then fail "the image embeds the placeholder index.html"; fi
grep -qi "^content-security-policy: .*'sha256-" "$TMP/headers" || fail "no CSP carrying the script hash"
pass "SPA deep link with runtime config and CSP"

asset="$(grep -o 'src="/assets/[^"]*\.js"' "$TMP/index" | head -n1 | cut -d'"' -f2)"
[ -n "$asset" ] || fail "no module script in the index"
curl -fsS -D "$TMP/asset-headers" -o /dev/null "$base$asset"
grep -qi '^cache-control: public, max-age=31536000, immutable' "$TMP/asset-headers" || fail "$asset is not cached as immutable"
pass "hashed asset $asset"

[ "$(curl -s -D "$TMP/api-headers" -o /dev/null -w '%{http_code}' "$base/api/v1/does-not-exist")" = 404 ] ||
  fail "an unknown API path is not 404"
grep -qi '^content-type: application/problem+json' "$TMP/api-headers" || fail "an unknown API path is not a problem document"
pass "API catch-all answers a 404 problem"

[ "$(status -X POST -H 'Origin: https://evil.example' -H 'Sec-Fetch-Site: cross-site' "$base/api/v1/anything")" = 403 ] ||
  fail "a cross-site POST was not rejected"
pass "cross-site POST rejected"

[ "$(status -H 'Host: evil.example' "$base/")" = 400 ] || fail "a foreign Host header was not rejected"
pass "foreign Host rejected"

docker exec "$APP" /app/vantigo healthcheck || fail "the healthcheck command failed inside the container"
health=""
for _ in $(seq 1 30); do
  health="$(docker inspect --format '{{.State.Health.Status}}' "$APP")"
  [ "$health" = healthy ] && break
  sleep 1
done
[ "$health" = healthy ] || fail "container health is '$health'"
pass "container HEALTHCHECK is healthy"

# --- worker ------------------------------------------------------------------------

docker run -d --name "$WORKER" --network "$NET" -p "127.0.0.1:$WORKER_PORT:8080" "${env_args[@]}" "$IMAGE" worker >/dev/null
wait_for "http://localhost:$WORKER_PORT/health/ready"
[ "$(status "http://localhost:$WORKER_PORT/")" = 404 ] || fail "worker serves more than health"
pass "worker serves health only"

echo "smoke test passed: $IMAGE"
```

- [ ] **Step 5: Lint the scripts**

Run: `chmod +x scripts/*.sh && shellcheck scripts/*.sh`
Expected: no output, exit 0.

- [ ] **Step 6: Build, then smoke-test the image (this is the test)**

Run:

```bash
bun install --frozen-lockfile
VANTIGO_VERSION=0.0.0-local bash scripts/build-artifacts.sh
git status --porcelain apps/server/internal/web   # expect no output: the placeholder is back
docker build -t vantigo:dev .
SMOKE_EXPECT_VERSION=0.0.0-local bash scripts/smoke-image.sh vantigo:dev
```

Expected: `build-artifacts.sh` lists two binaries of roughly 20 MB; `git status` prints nothing; the smoke test prints an `ok` line per check and ends with `smoke test passed: vantigo:dev`.

To confirm the smoke test can fail, run it once with a wrong expected version: `SMOKE_EXPECT_VERSION=9.9.9 bash scripts/smoke-image.sh vantigo:dev` must end with `SMOKE FAIL: version is not 9.9.9`, dump the container logs, and exit 1.

- [ ] **Step 7: Verify the multi-arch build**

Run: `SKIP_ARTIFACTS=1 bash scripts/build-image.sh`
Expected: `==> building vantigo:dev for linux/amd64,linux/arm64 (verify only; PUSH=1 publishes)`, then a successful buildx run.

- [ ] **Step 8: Commit**

```bash
git add .gitignore Dockerfile .dockerignore scripts
git commit -m "build(server): add native artifacts, the COPY-only image and a smoke test

Binaries are cross-compiled natively with the SPA embedded; the distroless
nonroot image only COPYs them. scripts/smoke-image.sh runs the shipped
image end to end: migrate, SPA, health, API catch-all, CSRF and host
rejection, the container HEALTHCHECK, and the worker command."
```

---

### Task 13: GoReleaser

**Files:**
- Create: `.goreleaser.yaml`

**Interfaces:**
- Consumes: `scripts/spa-embed-overlay.sh`, `scripts/restore-embed-overlay.sh`, `Dockerfile` (`BINARY_ROOT`) (Task 12).
- Produces: `goreleaser check` passes; `goreleaser release --snapshot --clean --skip=sign` builds archives, checksums, SBOMs and per-arch images under `dist/goreleaser`. The real publish is switched on at the cutover.

- [ ] **Step 1: Write the configuration**

Create `.goreleaser.yaml`:

```yaml
# GoReleaser owns everything downstream of a version tag: binaries, archives,
# checksums, SBOMs, cosign signatures, per-arch images plus the multi-arch
# manifest, and the GitHub Release with a Conventional-Commits changelog. The
# version decision stays outside (svu) — GoReleaser releases from the tag it
# finds.
#
# Until the cutover this runs only as a dry run (server-release.yml,
# `mise run snapshot`); releases are still the .NET pipeline's.
version: 2

project_name: vantigo

# Not ./dist: build-artifacts.sh writes dist/server there, and the before
# hook would trip GoReleaser's dist-must-be-empty check.
dist: dist/goreleaser

before:
  hooks:
    # The SPA must sit in the go:embed directory before the builds run.
    # Callers restore the placeholder afterwards (scripts/restore-embed-overlay.sh):
    # GoReleaser OSS has no after hooks.
    - bash scripts/spa-embed-overlay.sh

builds:
  - id: vantigo
    dir: apps/server
    main: ./cmd/vantigo
    binary: vantigo
    env:
      - CGO_ENABLED=0
    goos: [linux]
    goarch: [amd64, arm64]
    flags: [-trimpath]
    ldflags:
      - -s -w -X github.com/vantigo-io/vantigo/server/internal/buildinfo.Version={{ .Version }}

archives:
  - formats: [tar.gz]
    # vantigo_0.11.0_linux_amd64.tar.gz — the binary only; the SPA, the
    # migrations and tzdata are embedded in it.
    name_template: >-
      {{ .ProjectName }}_{{ .Version }}_{{ .Os }}_{{ .Arch }}

checksum:
  name_template: checksums.txt

# One SBOM per archive (syft, SPDX JSON).
sboms:
  - artifacts: archive

# Keyless cosign over the checksum file: verifying it plus the checksums
# transitively verifies every archive.
signs:
  - cmd: cosign
    artifacts: checksum
    output: true
    signature: "${artifact}.sigstore.json"
    args:
      - sign-blob
      - --yes
      - --bundle=${signature}
      - ${artifact}

dockers_v2:
  - images:
      - ghcr.io/vantigo-io/vantigo
    # The full version never moves; major.minor moves with patches; major with
    # minors; latest with every release. sha-<commit> stays for exact pinning.
    tags:
      - "{{ .Version }}"
      - "{{ .Major }}.{{ .Minor }}"
      - "{{ .Major }}"
      - latest
      - sha-{{ .FullCommit }}
    dockerfile: Dockerfile
    platforms:
      - linux/amd64
      - linux/arm64
    build_args:
      BINARY_ROOT: "."
    labels:
      org.opencontainers.image.source: https://github.com/vantigo-io/vantigo
      org.opencontainers.image.version: "{{ .Version }}"
      org.opencontainers.image.revision: "{{ .FullCommit }}"
      org.opencontainers.image.licenses: AGPL-3.0-or-later

docker_signs:
  - cmd: cosign
    artifacts: all
    output: true
    args:
      - sign
      - --yes
      - "${artifact}@${digest}"

changelog:
  sort: asc
  filters:
    exclude:
      - "^docs"
      - "^test"
      - "^chore"
      - "^ci"
      - "^style"
      - Merge pull request
      - Merge branch
  groups:
    - title: Features
      regexp: '^.*?feat(\([[:word:]-]+\))??!?:.+$'
      order: 0
    - title: Bug fixes
      regexp: '^.*?fix(\([[:word:]-]+\))??!?:.+$'
      order: 1
    - title: Performance
      regexp: '^.*?perf(\([[:word:]-]+\))??!?:.+$'
      order: 2
    - title: Other
      order: 999

release:
  github:
    owner: vantigo-io
    name: vantigo
  footer: |
    **Container image:** `ghcr.io/vantigo-io/vantigo:{{ .Version }}` (linux/amd64 + linux/arm64, cosign-signed)

snapshot:
  version_template: "{{ incpatch .Version }}-snapshot.{{ .ShortCommit }}"
```

- [ ] **Step 2: Validate it**

Run: `goreleaser check`
Expected: `• checking config…` then `• config is valid` (or `1 configuration file(s) validated`), exit 0.

- [ ] **Step 3: Run a snapshot**

Multi-platform image builds need a docker-container builder, and SBOMs need syft (from mise). `BUILDX_BUILDER` selects the builder for this one command only, so your default builder stays as it is:

```bash
docker buildx inspect vantigo >/dev/null 2>&1 || docker buildx create --name vantigo --driver docker-container
trap 'bash scripts/restore-embed-overlay.sh' EXIT
BUILDX_BUILDER=vantigo goreleaser release --snapshot --clean --skip=sign
ls dist/goreleaser
bash scripts/restore-embed-overlay.sh && git status --porcelain
```

Expected: `dist/goreleaser` contains `vantigo_<next>-snapshot.<sha>_linux_amd64.tar.gz`, the arm64 archive, `checksums.txt` and `*.sbom.json` files; the log shows both images built; `git status` prints nothing but the new `.goreleaser.yaml` if it isn't committed yet.

- [ ] **Step 4: Commit**

```bash
git add .goreleaser.yaml
git commit -m "build(server): add the GoReleaser configuration

Archives, checksums, SBOMs, keyless cosign, and the multi-arch image with
the version tag ladder. Dry-run only until the cutover."
```

---

### Task 14: CI workflows and Dependabot

**Files:**
- Create: `.github/workflows/server-test.yml`, `.github/workflows/server-ci.yml`, `.github/workflows/server-release.yml`
- Modify: `.github/dependabot.yml`

**Interfaces:**
- Consumes: `bun run toolchain:check`/`toolchain:test` (Task 1), `TEST_DATABASE_URL` (Task 6), `scripts/*.sh` (Task 12), `.goreleaser.yaml` (Task 13).
- Produces: a reusable Go gate (`server-test.yml`), the PR pipeline (`server-ci.yml`), a manual GoReleaser dry run (`server-release.yml`).

Action pins reuse the SHAs already in `ci.yml` (checkout, mise-action, login-action, upload-artifact) and Pjokk's for the ones Vantigo doesn't use yet (setup-buildx-action `37fe631…` v4.3.0, cache `55cc834…` v6.1.0). The `.NET` `ci.yml` isn't touched.

- [ ] **Step 1: Write the reusable gate**

Create `.github/workflows/server-test.yml`:

```yaml
name: Server tests

# The Go server's quality gate — lint, tests against a real PostgreSQL,
# vulnerability scan, and the release config — defined once and called by the
# PR pipeline and the release dry run. At the cutover it merges with the
# frontend jobs into a single test.yml.

on:
  workflow_call:

permissions:
  contents: read

jobs:
  test:
    name: 🧪 Go server lint and tests
    runs-on: ubuntu-latest
    services:
      postgres:
        image: postgres:18-alpine
        env:
          POSTGRES_USER: vantigo
          POSTGRES_PASSWORD: vantigo
          POSTGRES_DB: vantigo_test
        ports:
          - 5432:5432
        options: >-
          --health-cmd "pg_isready -U vantigo -d vantigo_test"
          --health-interval 5s
          --health-timeout 5s
          --health-retries 20
    env:
      TEST_DATABASE_URL: postgres://vantigo:vantigo@127.0.0.1:5432/vantigo_test?sslmode=disable
    steps:
      - name: 🛎️ Checkout codebase
        uses: actions/checkout@3d3c42e5aac5ba805825da76410c181273ba90b1 # v7.0.1
        with:
          persist-credentials: false

      - name: 💾 Set up toolchain
        uses: jdx/mise-action@3c2e0cf82a5b2e5249f0d3635a4d83d0ae861518 # v4.2.5
        with:
          install_args: "go bun aqua:golangci/golangci-lint aqua:goreleaser/goreleaser aqua:rhysd/actionlint aqua:koalaman/shellcheck"

      - name: 💾 Cache Go modules and builds
        uses: actions/cache@55cc8345863c7cc4c66a329aec7e433d2d1c52a9 # v6.1.0
        with:
          path: |
            ~/go/pkg/mod
            ~/.cache/go-build
          key: ${{ runner.os }}-go-${{ hashFiles('apps/server/go.sum') }}
          restore-keys: ${{ runner.os }}-go-

      - name: 🧰 Install frontend dependencies
        run: bun install --frozen-lockfile

      - name: 🧰 Toolchain pins agree
        run: |
          bun run toolchain:check
          bun run toolchain:test

      - name: 🔍 Lint workflows and scripts
        run: |
          actionlint .github/workflows/server-*.yml
          shellcheck scripts/*.sh

      - name: 🔍 golangci-lint
        working-directory: apps/server
        run: golangci-lint run

      - name: 🧪 Go tests with the race detector
        working-directory: apps/server
        run: go test -race -count=1 ./...

      - name: 🛡️ govulncheck
        working-directory: apps/server
        run: go run golang.org/x/vuln/cmd/govulncheck@v1.7.0 ./...

      - name: 🔍 GoReleaser configuration
        run: goreleaser check
```

- [ ] **Step 2: Write the PR pipeline**

Create `.github/workflows/server-ci.yml`:

```yaml
name: Server CI

# The Go server's PR pipeline while the port is in progress: the full gate
# (server-test.yml), then the image built once from native artifacts,
# smoke-tested, scanned, and pushed as a semver-prerelease preview
# (<next>-pr.<n>, <next>-pr.<n>.<sha>) — never latest or a release tag. The
# .NET pipeline in ci.yml is untouched; at the cutover the two merge into
# Pjokk's test.yml / ci.yml / release.yml trio.

on:
  pull_request:

permissions:
  contents: read

# A force-push obsoletes the running check.
concurrency:
  group: server-ci-${{ github.head_ref }}
  cancel-in-progress: true

jobs:
  test:
    uses: ./.github/workflows/server-test.yml
    permissions:
      contents: read

  image:
    name: 📦 Image, smoke test and preview
    needs: test
    runs-on: ubuntu-latest
    permissions:
      contents: read
      packages: write
    env:
      IMAGE: ghcr.io/vantigo-io/vantigo
    steps:
      - name: 🛎️ Checkout codebase
        uses: actions/checkout@3d3c42e5aac5ba805825da76410c181273ba90b1 # v7.0.1
        with:
          # svu computes the upcoming version from tags and history.
          fetch-depth: 0
          persist-credentials: false

      - name: 💾 Set up toolchain
        uses: jdx/mise-action@3c2e0cf82a5b2e5249f0d3635a4d83d0ae861518 # v4.2.5
        with:
          install_args: "go bun trivy aqua:caarlos0/svu"

      - name: 💾 Cache Go modules and builds
        uses: actions/cache@55cc8345863c7cc4c66a329aec7e433d2d1c52a9 # v6.1.0
        with:
          path: |
            ~/go/pkg/mod
            ~/.cache/go-build
          key: ${{ runner.os }}-go-${{ hashFiles('apps/server/go.sum') }}
          restore-keys: ${{ runner.os }}-go-

      - name: 🧰 Install frontend dependencies
        run: bun install --frozen-lockfile

      - name: 🧮 Compute the preview version
        id: version
        env:
          HEAD_SHA: ${{ github.event.pull_request.head.sha }}
          PR_NUMBER: ${{ github.event.pull_request.number }}
        run: |
          tag="$(svu next --v0)"
          next="${tag#v}"
          {
            echo "next=$next"
            echo "moving=$next-pr.$PR_NUMBER"
            echo "pinned=$next-pr.$PR_NUMBER.${HEAD_SHA:0:7}"
          } >> "$GITHUB_OUTPUT"

      - name: 🏗️ Build native artifacts
        env:
          VANTIGO_VERSION: ${{ steps.version.outputs.pinned }}
        run: bash scripts/build-artifacts.sh

      - name: 📦 Build the image
        run: docker build -t vantigo:ci .

      - name: 🔥 Smoke-test the image
        env:
          SMOKE_EXPECT_VERSION: ${{ steps.version.outputs.pinned }}
        run: bash scripts/smoke-image.sh vantigo:ci

      - name: 🛡️ Scan the image for vulnerabilities
        run: trivy image --scanners vuln --severity HIGH,CRITICAL --exit-code 1 --format table vantigo:ci

      # A fork's PR gets a read-only token; pushing would fail for reasons
      # unrelated to the change.
      - name: 🔀 Can this run push?
        id: gate
        env:
          HEAD_REPO: ${{ github.event.pull_request.head.repo.full_name }}
        run: |
          if [ "$HEAD_REPO" = "$GITHUB_REPOSITORY" ]; then
            echo "push=true" >> "$GITHUB_OUTPUT"
          else
            echo "push=false" >> "$GITHUB_OUTPUT"
          fi

      - name: 🧱 Set up buildx
        if: steps.gate.outputs.push == 'true'
        uses: docker/setup-buildx-action@37fe631027851001ddb9b187196cc803df7f5f0e # v4.3.0

      - name: 🔐 Log in to GHCR
        if: steps.gate.outputs.push == 'true'
        uses: docker/login-action@dbcb813823bdd20940b903addbd779551569679f # v4.6.0
        with:
          registry: ghcr.io
          username: ${{ github.actor }}
          password: ${{ secrets.GITHUB_TOKEN }}

      - name: 📤 Push the preview image (linux/amd64 + linux/arm64)
        if: steps.gate.outputs.push == 'true'
        env:
          MOVING: ${{ steps.version.outputs.moving }}
          PINNED: ${{ steps.version.outputs.pinned }}
        run: |
          docker buildx build --platform linux/amd64,linux/arm64 \
            -t "$IMAGE:$MOVING" -t "$IMAGE:$PINNED" --push .
          platforms="$(docker buildx imagetools inspect --raw "$IMAGE:$PINNED" \
            | jq -r '.manifests[].platform | select(.os != "unknown") | .os + "/" + .architecture' | sort -u)"
          for expected in linux/amd64 linux/arm64; do
            echo "$platforms" | grep -qx "$expected" || { echo "::error::The pushed index is missing $expected."; exit 1; }
          done
          {
            echo "### Go server preview image"
            echo
            echo '```'
            echo "docker pull $IMAGE:$MOVING"
            echo '```'
            echo
            echo "Also tagged \`$PINNED\` (immutable)."
          } >> "$GITHUB_STEP_SUMMARY"
```

- [ ] **Step 3: Write the release dry run**

Create `.github/workflows/server-release.yml`:

```yaml
name: Server release (dry run)

# Runs the GoReleaser pipeline end to end — binaries, archives, checksums,
# SBOMs and the multi-arch images — without tagging, pushing or signing, and
# proves the cosign invocations on a throwaway blob (GoReleaser skips signing
# in snapshot mode, which is how a flag incompatibility would otherwise first
# show up during a real release). Releases stay with ci.yml's .NET pipeline
# until the cutover, when this becomes Pjokk's push-to-main release.yml.

on:
  workflow_dispatch:

permissions:
  contents: read

jobs:
  test:
    uses: ./.github/workflows/server-test.yml
    permissions:
      contents: read

  snapshot:
    name: 📦 GoReleaser snapshot
    needs: test
    runs-on: ubuntu-latest
    permissions:
      contents: read
      id-token: write # keyless cosign for the flag smoke test
    steps:
      - name: 🛎️ Checkout codebase
        uses: actions/checkout@3d3c42e5aac5ba805825da76410c181273ba90b1 # v7.0.1
        with:
          fetch-depth: 0
          persist-credentials: false

      - name: 💾 Set up toolchain
        uses: jdx/mise-action@3c2e0cf82a5b2e5249f0d3635a4d83d0ae861518 # v4.2.5
        with:
          install_args: "go bun aqua:goreleaser/goreleaser aqua:sigstore/cosign aqua:anchore/syft"

      - name: 💾 Cache Go modules and builds
        uses: actions/cache@55cc8345863c7cc4c66a329aec7e433d2d1c52a9 # v6.1.0
        with:
          path: |
            ~/go/pkg/mod
            ~/.cache/go-build
          key: ${{ runner.os }}-go-${{ hashFiles('apps/server/go.sum') }}
          restore-keys: ${{ runner.os }}-go-

      - name: 🧰 Install frontend dependencies
        run: bun install --frozen-lockfile

      # Multi-platform builds with SBOM attestations need the docker-container driver.
      - name: 🧱 Set up buildx
        uses: docker/setup-buildx-action@37fe631027851001ddb9b187196cc803df7f5f0e # v4.3.0

      - name: 📦 GoReleaser (snapshot)
        run: |
          trap 'bash scripts/restore-embed-overlay.sh' EXIT
          goreleaser release --snapshot --clean --skip=sign

      - name: 🔏 Cosign flags smoke test
        run: |
          echo smoke > "$RUNNER_TEMP/cosign-smoke.txt"
          cosign sign-blob --yes --bundle="$RUNNER_TEMP/cosign-smoke.sigstore.json" "$RUNNER_TEMP/cosign-smoke.txt"
          cosign verify-blob \
            --bundle "$RUNNER_TEMP/cosign-smoke.sigstore.json" \
            --certificate-identity-regexp "https://github.com/${GITHUB_REPOSITORY}/\.github/workflows/server-release\.yml@.*" \
            --certificate-oidc-issuer https://token.actions.githubusercontent.com \
            "$RUNNER_TEMP/cosign-smoke.txt"

      - name: 📤 Upload the snapshot
        uses: actions/upload-artifact@043fb46d1a93c77aae656e7c1c64a875d1fc6a0a # v7.0.1
        with:
          name: goreleaser-snapshot
          path: |
            dist/goreleaser/*.tar.gz
            dist/goreleaser/checksums.txt
            dist/goreleaser/*.sbom.json
          if-no-files-found: error
          retention-days: 14
```

- [ ] **Step 4: Add the Go and Docker ecosystems to Dependabot**

In `.github/dependabot.yml`, append to `updates:`:

```yaml
  - package-ecosystem: gomod
    directory: /apps/server
    multi-ecosystem-group: all-dependencies
    patterns: ['*']
  - package-ecosystem: docker
    directory: /
    multi-ecosystem-group: all-dependencies
    patterns: ['*']
```

- [ ] **Step 5: Lint the workflows (this is the test)**

Run: `actionlint .github/workflows/server-*.yml && goreleaser check`
Expected: no actionlint output, exit 0 (actionlint runs shellcheck over every `run:` block); GoReleaser config valid.

To confirm the linter is actually watching, temporarily change `needs: test` to `needs: tests` in `server-ci.yml` and rerun. actionlint must report `job "image" needs job "tests" which does not exist`. Revert the change.

- [ ] **Step 6: Commit**

```bash
git add .github/workflows/server-test.yml .github/workflows/server-ci.yml .github/workflows/server-release.yml .github/dependabot.yml
git commit -m "ci(server): gate, smoke-test and preview-publish the Go server

server-test.yml is the reusable Go gate; server-ci.yml builds the image
from native artifacts on every PR, smoke-tests and scans it, and pushes
semver-prerelease preview tags; server-release.yml is a manual GoReleaser
dry run. Dependabot now watches go.mod and the base image."
```

---

### Task 15: Local development workflow and contributor docs

**Files:**
- Create: `docker-compose.dev.yml`
- Modify: `mise.toml` (tasks), `.husky/pre-commit` (gofmt gate), `CONTRIBUTING.md` (new section before `## Commit conventions`)

**Interfaces:**
- Consumes: everything above.
- Produces: `mise run server:db | server:test | server:check | server:dev | artifacts | image | smoke | snapshot`.

- [ ] **Step 1: Add a development database**

Create `docker-compose.dev.yml`:

```yaml
# PostgreSQL for running the Go server locally (`mise run server:dev`):
#
#   docker compose -f docker-compose.dev.yml up -d --wait
#
# Kept apart from docker-compose.test.yml, whose server is throwaway and
# holds only per-test databases.
name: vantigo-dev

services:
  postgres:
    image: postgres:18-alpine
    environment:
      POSTGRES_USER: vantigo
      POSTGRES_PASSWORD: vantigo
      POSTGRES_DB: vantigo
    ports:
      - "127.0.0.1:55433:5432"
    volumes:
      - postgres-data:/var/lib/postgresql
    healthcheck:
      test: ["CMD-SHELL", "pg_isready -U vantigo -d vantigo"]
      interval: 2s
      timeout: 3s
      retries: 30

volumes:
  postgres-data:
```

- [ ] **Step 2: Add mise tasks**

Append to `mise.toml`:

```toml
# ─── Go server tasks: `mise run <task>` ─────────────────────────────────────

[tasks."server:db"]
description = "Start PostgreSQL for Go tests (127.0.0.1:55432) and local development (127.0.0.1:55433)"
run = [
  "docker compose -f docker-compose.test.yml up -d --wait",
  "docker compose -f docker-compose.dev.yml up -d --wait",
]

[tasks."server:test"]
description = "Go tests against docker-compose.test.yml's PostgreSQL (CI adds -race)"
dir = "apps/server"
run = "go test -count=1 ./..."

[tasks."server:check"]
description = "golangci-lint, govulncheck, shellcheck, actionlint and the GoReleaser config"
run = [
  "cd apps/server && golangci-lint run",
  "cd apps/server && go run golang.org/x/vuln/cmd/govulncheck@v1.7.0 ./...",
  "shellcheck scripts/*.sh",
  "actionlint .github/workflows/server-*.yml",
  "goreleaser check",
]

[tasks."server:dev"]
description = "Run the api command against docker-compose.dev.yml on http://localhost:8080"
dir = "apps/server"
env = { APP_ENV = "development", APP_URL = "http://localhost:8080", LOG_LEVEL = "debug", DATABASE_URL = "postgres://vantigo:vantigo@127.0.0.1:55433/vantigo?sslmode=disable" }
run = "go run ./cmd/vantigo api"

[tasks.artifacts]
description = "SPA + native server binaries → dist/server/linux/<arch>/vantigo"
run = "bash scripts/build-artifacts.sh"

[tasks.image]
description = "Multi-arch image via buildx (verify only; PUSH=1 TAG=… publishes)"
run = "bash scripts/build-image.sh"

[tasks.smoke]
description = "Build the artifacts and a local image, then smoke-test it end to end"
run = [
  "bash scripts/build-artifacts.sh",
  "docker build -t vantigo:dev .",
  "bash scripts/smoke-image.sh vantigo:dev",
]

[tasks.snapshot]
description = "GoReleaser dry run: archives, SBOMs, local images (no publish, no signing)"
run = """
trap 'bash scripts/restore-embed-overlay.sh' EXIT
docker buildx inspect vantigo >/dev/null 2>&1 || docker buildx create --name vantigo --driver docker-container
BUILDX_BUILDER=vantigo goreleaser release --snapshot --clean --skip=sign
"""
```

- [ ] **Step 3: Gate unformatted Go in the pre-commit hook**

Append to `.husky/pre-commit`:

```sh

printf '%s\n' 'Running: gofmt -l apps/server'
unformatted=$(run gofmt -l apps/server)
if [ -n "$unformatted" ]; then
  printf 'These Go files need `gofmt -w`:\n%s\n' "$unformatted"
  exit 1
fi
```

- [ ] **Step 4: Document the workflow**

In `CONTRIBUTING.md`, insert this section directly before the `## Commit conventions` heading:

````markdown
## Go server (port in progress)

The .NET backend is being replaced by a single Go binary in `apps/server`
(design: `docs/superpowers/specs/2026-09-10-go-backend-port-design.md`). Until the
cutover the .NET host is what ships; the Go server is built, smoke-tested and
published as PR preview images by the `server-*.yml` workflows.

```bash
mise install              # Go, lint and release tools
mise run server:db        # PostgreSQL for tests (55432) and development (55433)
mise run server:test      # go test against a real PostgreSQL
mise run server:check     # golangci-lint, govulncheck, shellcheck, actionlint, goreleaser check
mise run server:dev       # the api command on http://localhost:8080
mise run smoke            # build the image and smoke-test it end to end
```

Rules the code relies on:

- `cmd/vantigo` is the only composition root. Nothing below it reads
  `os.Getenv`; new settings go in `internal/config`, which reports every
  problem at once.
- Tests hit a real PostgreSQL. `internal/testdb` gives each test its own
  database, so tests never share tables and packages run in parallel.
- Error responses are RFC 7807 problems from `internal/httpx`. Nothing from an
  internal error reaches a response body.
- The image is COPY-only: `scripts/build-artifacts.sh` compiles natively and
  embeds the SPA; the Dockerfile never compiles anything.

````

- [ ] **Step 5: Verify the workflow end to end (this is the test)**

Run:

```bash
mise tasks | grep -E 'server:|artifacts|image|smoke|snapshot'
mise run server:db
mise run server:test
mise run server:check
bun run toolchain:check
printf 'package main\nfunc  x() {}\n' > apps/server/cmd/vantigo/zz_unformatted.go
git add apps/server/cmd/vantigo/zz_unformatted.go && git commit -m "test: gofmt gate" ; echo "exit=$?"
git reset -q HEAD apps/server/cmd/vantigo/zz_unformatted.go && rm apps/server/cmd/vantigo/zz_unformatted.go
```

Expected: the eight tasks are listed; both databases report `Healthy`; every Go test passes; `server:check` reports no issues; the toolchain check passes; the deliberately unformatted commit is refused with `These Go files need gofmt -w: apps/server/cmd/vantigo/zz_unformatted.go` and `exit=1`. The file is then removed.

- [ ] **Step 6: Commit**

```bash
git add docker-compose.dev.yml mise.toml .husky/pre-commit CONTRIBUTING.md
git commit -m "docs(server): add the Go server's local workflow

mise tasks for the databases, tests, checks, the dev server, artifacts,
image, smoke test and GoReleaser snapshot; a gofmt gate in pre-commit; and
a CONTRIBUTING section on how the port is built and tested."
```

---

## Self-review against the spec

| Spec requirement (sub-project 1, §10 and the sections it references) | Task |
|---|---|
| `apps/server` module, `buildinfo` | 1 |
| env config, all problems at once, fail-closed transport (§3.3, §3.11) | 2 |
| `httpx`: ProblemDetails shape, sanitised errors, trace id, recovery, request log, forwarded headers, base path (§3.5) | 3 |
| security headers byte-for-byte, CSP report-only, HSTS, host filtering (§3.11) | 4 |
| embedded SPA, index templating, CSP hash from the injected script, SPA fallback (§3.12) | 5 |
| pgx pool, goose, advisory lock `0x56414E5449474F31`, `NNNNN_<owner>_<name>.sql` (§3.4) | 6 |
| rate limiter with the .NET rejection shape (§3.7, platform part) | 7 |
| `/health/live`, `/health/ready`, `healthcheck` probe (§3.12) | 8 |
| middleware order and `/api` catch-all, CSRF (§3.5, §3.11) | 9 |
| dispatch table, composition root, migrate-before-listen, graceful drain (§3.2) | 10 |
| OTel traces/metrics/logs only when configured, slog (§3.13) | 11 |
| native build, COPY-only distroless nonroot image, HEALTHCHECK, smoke test (§5, §6) | 12 |
| GoReleaser: archives, SBOMs, cosign, multi-arch image, tag ladder (§5) | 13 |
| CI: reusable gate, PR image + smoke + trivy + preview push, release dry run, non-root and platform assertions (§6) | 14 |
| dev workflow: compose, mise tasks (§8) | 15 |

Deliberately outside this plan: modules, sessions and everything else from sub-projects 2–7; the real push-to-main release (at the cutover); a pruning job for stale `platform.rate_limit` rows (added with the worker runtime in sub-project 5); Mailpit in the dev compose file (added with SMTP in sub-project 5).

