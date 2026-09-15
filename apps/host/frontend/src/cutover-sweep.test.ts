import { execFileSync } from "node:child_process";
import { existsSync, readFileSync } from "node:fs";
import { dirname, join, relative } from "node:path";
import { fileURLToPath } from "node:url";
import { describe, expect, it } from "vitest";

// The .NET -> Go cutover's last step (docs/superpowers/specs/
// 2026-09-14-cutover-design.md): the repository-wide check that the deleted .NET
// build system left behind nothing a tool or a person could still act on.
//
// WHAT THIS SWEEP CANNOT DO — read this before trusting a green run. It
// matches tokens. It cannot tell that a documented route does not exist, that
// a list of background workers names one the server never runs, or that the
// docs describe an endpoint that was never implemented. All three happened
// during this cutover, none of them matched any token below, and every one was
// found by a person reading prose. Green here means "no banned token
// survives". It does not mean the repository is truthful.
//
// SCOPE IT BY PRINCIPLE, NEVER BY ALLOWLIST. The scope is every tracked file,
// minus two things excluded for a reason rather than because they are known to
// be red: docs/superpowers/** (historical design records, which are *supposed*
// to name what was removed) and this file (which must spell the tokens in
// order to ban them). Untracked and ignored files are out of scope because
// they do not ship; git history is out of scope because the sweep reads the
// working tree. Do not add an exception list here — the next plausible-looking
// hit is how a sweep like this stops meaning anything.
const THIS_FILE = fileURLToPath(import.meta.url);
// THIS_FILE is .../apps/host/frontend/src/cutover-sweep.test.ts; walk up four
// levels (src -> frontend -> host -> apps) to the repo root.
const REPO_ROOT = join(dirname(THIS_FILE), "..", "..", "..", "..");
const SELF = relative(REPO_ROOT, THIS_FILE);

const EXCLUDED_PREFIXES = ["docs/superpowers/"] as const;

type BannedToken = { readonly what: string; readonly pattern: RegExp };

// Every pattern matches case-insensitively. A leftover does not have to keep
// the casing the .NET tree happened to use — a lowercased `dotnet_root`, a
// shouted `GLOBAL.JSON` or an `apphost:` key is the same leftover, and a
// case-sensitive pattern would wave all three through. The boundaries below do
// the narrowing instead of letter case, which is why widening to /i costs none
// of the deliberate allowances: the mustNotMatch samples in the first test pin
// that in the other direction.
const BANNED_TOKENS: readonly BannedToken[] = [
  // Word-bounded on both sides: the Go SCIM port's dotnetTrim/dotnetBlank
  // helpers name .NET *semantics* it reproduces, not a toolchain to install.
  { what: "a dotnet CLI invocation", pattern: /\bdotnet\b/i },
  // Separately from the word-bounded form above, because a trailing underscore
  // is a word character and so \bdotnet\b would never fire on DOTNET_ROOT.
  { what: "a DOTNET_* environment variable", pattern: /\bdotnet_[a-z0-9]/i },
  { what: "the deleted Vantigo.slnx solution", pattern: /vantigo\.slnx/i },
  { what: "a global.json .NET SDK pin", pattern: /global\.json/i },
  // Letter boundaries rather than \b, in both directions and for different
  // reasons. A trailing letter must still exclude: config.AppHostname is the
  // Go server's own field. A leading underscore must NOT exclude, which \b
  // gets wrong — "_" is a word character, so \bAppHost\b never fires inside
  // Vantigo_AppHost.csproj and the project file walks straight through.
  { what: "the deleted Aspire AppHost project", pattern: /(?<![a-z])apphost(?![a-z])/i },
  { what: "the deleted orchestration/ tree", pattern: /orchestration\//i },
  // A configuration key has a name after the separator. A bare
  // `ConnectionStrings__` is prose, or the compose job's grep for this very
  // thing — neither is a key the Go server could be asked to read.
  { what: "a ConnectionStrings__ configuration key", pattern: /connectionstrings__[a-z0-9]/i },
];

const trackedFiles = (root: string): string[] => {
  // Without this, a wrong root makes `git ls-files` report the files of
  // whatever repository encloses it — or nothing at all — and the sweep passes
  // by scanning an empty set. That exact false-green has happened on this
  // project, so refusing is the behaviour under test below.
  if (!existsSync(join(root, ".git"))) {
    throw new Error(
      `cutover sweep: ${root} is not a git working tree — refusing to scan, a green run would be vacuous`,
    );
  }
  const listing = execFileSync("git", ["-C", root, "ls-files", "-z"], {
    encoding: "utf8",
    maxBuffer: 256 * 1024 * 1024,
  });
  const paths = listing.split("\0").filter((path) => path.length > 0);
  if (paths.length === 0) throw new Error(`cutover sweep: git ls-files reported no tracked files under ${root}`);
  return paths;
};

const scannedFiles = () =>
  trackedFiles(REPO_ROOT).filter(
    (path) => path !== SELF && !EXCLUDED_PREFIXES.some((prefix) => path.startsWith(prefix)),
  );

const miseTask = (name: string): string => {
  const toml = readFileSync(join(REPO_ROOT, "mise.toml"), "utf8");
  const header = `[tasks."${name}"]`;
  const start = toml.indexOf(header);
  if (start < 0) throw new Error(`cutover sweep: mise.toml no longer declares ${header}`);
  const body = toml.slice(start + header.length);
  const end = body.indexOf("\n[");
  return end < 0 ? body : body.slice(0, end);
};

const localhostPort = (source: string, pattern: RegExp, where: string): string => {
  const port = pattern.exec(source)?.[1];
  if (port === undefined) throw new Error(`cutover sweep: could not read a http://localhost:<port> from ${where}`);
  return port;
};

describe("cutover sweep", () => {
  it("matches what it claims to match, and lets through what it deliberately allows", () => {
    // A pattern that silently excludes part of its intended scope is
    // indistinguishable from a clean repository. An earlier check on this
    // branch read as clean for exactly that reason: its SCREAMING_SNAKE regex
    // skipped every STORAGE_S3_* and OTEL_EXPORTER_OTLP_* key, because a
    // trailing wildcard defeats a word boundary. These samples prove both
    // directions — what must be caught, and what the boundaries above let
    // through on purpose — before any clean result is believed.
    const mustMatch = [
      'dotnet format "Vantigo.slnx" --verify-no-changes --no-restore',
      "  run: dotnet publish -c Release",
      "## Get latest from `dotnet new gitignore`",
      "      DOTNET_NOLOGO: 1",
      '  <Solution Include="Vantigo.slnx" />',
      "COPY global.json ./",
      "COPY orchestration/ /src/orchestration/",
      "      AppHost: true",
      "ConnectionStrings__DefaultConnection=Host=db;Database=vantigo",
      // Case variants, one per pattern that letter case could have hidden.
      // Each is written so that only the pattern it is pinning can match it —
      // no bare `dotnet` word to be caught by the first pattern instead — so a
      // pattern that silently lost its /i reddens here rather than going quiet.
      "      dotnet_nologo: 1",
      "COPY GLOBAL.JSON ./",
      "CONNECTIONSTRINGS__DEFAULTCONNECTION=Host=db;Database=vantigo",
      "      apphost: true",
      '  <ProjectReference Include="Vantigo_AppHost.csproj" />',
      '  <Solution Include="vantigo.slnx" />',
    ];
    const mustNotMatch = [
      "s := dotnetTrim(*v)",
      "if dotnetBlank(value) {",
      "const dotnetSpace = `[\\f\\n\\r\\t\\v\\x{85}\\p{Z}]`",
      "BenchmarkDotNet.Artifacts/",
      "AppHostname string",
      "c.AppOrigin, c.AppHostname = appOrigin(&p, env)",
      "if grep -n 'Vantigo\\.Host\\|ConnectionStrings__' compose.yaml; then",
      'echo "::error::found a leftover .NET binary name or ConnectionStrings__ key"',
    ];
    const matches = (sample: string) => BANNED_TOKENS.some(({ pattern }) => pattern.test(sample));

    expect(mustMatch.filter((sample) => !matches(sample))).toEqual([]);
    expect(mustNotMatch.filter(matches)).toEqual([]);
    // And every pattern is exercised: one that no sample reaches is a pattern
    // nobody has shown fires at all.
    expect(
      BANNED_TOKENS.filter(({ pattern }) => !mustMatch.some((sample) => pattern.test(sample))).map(({ what }) => what),
    ).toEqual([]);
  });

  it("refuses to scan a path that is not a git working tree", () => {
    expect(() => trackedFiles(join(REPO_ROOT, "no-such-directory"))).toThrow(/refusing to scan/);
  });

  it("scans the whole tracked tree rather than a handful of files", () => {
    // Guards against the sweep passing vacuously because the scope resolved to
    // far fewer files than the repository actually tracks.
    expect(scannedFiles().length).toBeGreaterThan(500);
  });

  it("finds no surviving dotnet, Vantigo.slnx, global.json, AppHost, orchestration/ or ConnectionStrings__", () => {
    const hits: string[] = [];
    for (const path of scannedFiles()) {
      const content = readFileSync(join(REPO_ROOT, path), "utf8");
      for (const { what, pattern } of BANNED_TOKENS) {
        if (!pattern.test(content)) continue;
        content.split("\n").forEach((line, index) => {
          if (pattern.test(line)) hits.push(`${path}:${index + 1}: ${what}`);
        });
      }
    }
    expect(hits).toEqual([]);
  });

  it("points the vite dev proxy at the port mise's server:dev task serves", () => {
    // Runtime reachability cannot be pinned from this suite. Drift can: the
    // two silently disagreeing — someone moves server:dev's port and the proxy
    // keeps pointing at the old one — is the failure this sweep exists to stop.
    const proxyPort = localhostPort(
      readFileSync(join(REPO_ROOT, "apps/host/frontend/vite.config.ts"), "utf8"),
      /const apiTarget = "http:\/\/localhost:(\d+)"/,
      "apps/host/frontend/vite.config.ts",
    );
    const serverPort = localhostPort(
      miseTask("server:dev"),
      /APP_URL = "http:\/\/localhost:(\d+)"/,
      'mise.toml [tasks."server:dev"] APP_URL',
    );
    expect(proxyPort).toBe(serverPort);
  });
});
