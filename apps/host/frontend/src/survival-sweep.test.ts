import { existsSync, readdirSync, readFileSync, statSync } from "node:fs";
import { dirname, join, relative } from "node:path";
import { fileURLToPath } from "node:url";
import { describe, expect, it } from "vitest";

// Task 9 of the frontend de-tenanting plan (docs/superpowers/specs/
// 2026-09-14-frontend-detenanting-design.md §3): the one check that catches a
// dormant tenant prefix or a reintroduced tenant switcher, because no test in
// the frontend suite hits a real Go server and string-level assertions are
// all there is otherwise.
//
// SCOPE IT BY PRINCIPLE, NEVER BY ALLOWLIST. This sweep gets zero exceptions.
// Scope below is `apps/*/frontend/src` and `packages/frontend-*/src`, minus
// generated files (routeTree.gen.ts, api-schema.d.ts) and this test itself —
// excluded as SCOPE, not as carve-outs for known-good hits. If a future
// change needs a real occurrence of one of these strings, it belongs outside
// this scope (e.g. documentation) or the string does not belong in shipped
// code at all. Do not add an exception list here: the next plausible-looking
// hit is how a sweep like this stops meaning anything.
const THIS_FILE = fileURLToPath(import.meta.url);
// THIS_FILE is .../apps/host/frontend/src/survival-sweep.test.ts; walk up
// four levels (src -> frontend -> host -> apps) to the repo root.
const REPO_ROOT = join(dirname(THIS_FILE), "..", "..", "..", "..");

const EXCLUDED_FILENAMES = new Set(["routeTree.gen.ts", "api-schema.d.ts"]);

const BANNED_STRINGS = ["tenantSlug", "/api/v1/t/", "ShellTenant", "onTenantSwitch", "tenantCatalog"] as const;

const listSourceDirs = (): string[] => {
  const dirs: string[] = [];

  const appsDir = join(REPO_ROOT, "apps");
  for (const app of readdirSync(appsDir)) {
    const src = join(appsDir, app, "frontend", "src");
    if (existsSync(src)) dirs.push(src);
  }

  const packagesDir = join(REPO_ROOT, "packages");
  for (const pkg of readdirSync(packagesDir)) {
    if (!pkg.startsWith("frontend-")) continue;
    const src = join(packagesDir, pkg, "src");
    if (existsSync(src)) dirs.push(src);
  }

  return dirs;
};

const listFiles = (dir: string): string[] => {
  const out: string[] = [];
  for (const entry of readdirSync(dir)) {
    const full = join(dir, entry);
    const stats = statSync(full);
    if (stats.isDirectory()) out.push(...listFiles(full));
    else if (stats.isFile()) out.push(full);
  }
  return out;
};

const scannedFiles = () =>
  listSourceDirs()
    .flatMap(listFiles)
    .filter((filePath) => filePath !== THIS_FILE && !EXCLUDED_FILENAMES.has(filePath.split("/").pop() ?? ""));

describe("survival sweep", () => {
  it("scans a non-trivial number of shipped frontend source files", () => {
    // Guards against the sweep passing vacuously because a path resolved
    // wrong and walked zero (or too few) files — a silent false-green that
    // would defeat the entire point of this test.
    expect(scannedFiles().length).toBeGreaterThan(100);
  });

  it("finds none of tenantSlug, /api/v1/t/, ShellTenant, onTenantSwitch, or tenantCatalog", () => {
    const hits: string[] = [];
    for (const filePath of scannedFiles()) {
      const content = readFileSync(filePath, "utf8");
      for (const needle of BANNED_STRINGS) {
        if (content.includes(needle)) hits.push(`${relative(REPO_ROOT, filePath)}: contains "${needle}"`);
      }
    }
    expect(hits).toEqual([]);
  });
});
