#!/usr/bin/env bun
import { readdirSync, readFileSync, statSync } from "node:fs";
import { join, relative, resolve } from "node:path";
/**
 * Documentation coverage check.
 *
 * Every page under docs/src/content/docs/en declares, in its `sources`
 * frontmatter, the repository paths it documents. Given a git range, this
 * script finds the changed files, the pages whose sources cover each of them,
 * and fails when a covered file changed while none of its pages did. It also
 * fails when a user/ or admin/ page changed in one language but not the other.
 *
 * A commit in the range may carry a trailer that waives the check:
 *
 *   Docs-Impact: none — a pure refactor, no behaviour change
 *
 * The reason is mandatory; the trailer is read by reviewers.
 *
 * Usage:
 *   bun run tools/docs/check-coverage.ts [--base <ref>] [--head <ref>]
 *   bun run tools/docs/check-coverage.ts --list        # print the coverage map
 *
 * Defaults: --base origin/main (or main), --head HEAD. In GitHub Actions the
 * base is the pull request's base ref.
 */
import { $ } from "bun";

const repositoryRoot = resolve(import.meta.dir, "../..");
const contentRoot = join(repositoryRoot, "docs/src/content/docs");
const defaultLocale = "en";
const locales = ["en", "nb"] as const;
/** Sections that must exist in every locale; the rest fall back to English. */
const bilingualSections = ["user", "admin"];
/** Changes here never need documentation on their own. */
const ignoredPrefixes = [
  "docs/",
  ".github/",
  ".husky/",
  "tools/",
  "scripts/",
  "orchestration/",
  ".omc/",
  "assets/",
  "node_modules/",
];
const ignoredFiles = new Set([
  "bun.lock",
  "package.json",
  "biome.json",
  "mise.toml",
  ".gitignore",
  ".editorconfig",
  ".dockerignore",
  "LICENSE",
  "AGENTS.md",
  "CLAUDE.md",
  "ROADMAP.md",
  "CONTRIBUTING.md",
  "README.md",
]);
const ignoredSuffixes = ["_test.go", ".test.ts", ".test.tsx", ".md"];

export type Page = { path: string; sources: string[] };

const walk = (dir: string): string[] =>
  readdirSync(dir).flatMap((name) => {
    const full = join(dir, name);
    if (statSync(full).isDirectory()) return walk(full);
    return /\.(md|mdx)$/.test(name) ? [full] : [];
  });

/** A minimal frontmatter reader: `sources:` as a YAML block list or inline array. */
export const readSources = (text: string): string[] => {
  const match = text.match(/^---\n([\s\S]*?)\n---/);
  if (!match) return [];
  const lines = match[1].split("\n");
  const start = lines.findIndex((l) => /^sources:/.test(l));
  if (start < 0) return [];
  const inline = lines[start].match(/^sources:\s*\[(.*)\]\s*$/);
  if (inline)
    return inline[1]
      .split(",")
      .map((s) => s.trim().replace(/^["']|["']$/g, ""))
      .filter(Boolean);
  const sources: string[] = [];
  for (const line of lines.slice(start + 1)) {
    const item = line.match(/^\s+-\s+["']?([^"'\s]+)["']?\s*$/);
    if (!item) break;
    sources.push(item[1]);
  }
  return sources;
};

export const loadPages = (): Page[] =>
  walk(join(contentRoot, defaultLocale)).map((full) => ({
    path: relative(repositoryRoot, full),
    sources: readSources(readFileSync(full, "utf8")).map((s) => s.replace(/\/+$/, "")),
  }));

const covers = (source: string, file: string) => file === source || file.startsWith(`${source}/`);

const needsDocs = (file: string) =>
  !ignoredPrefixes.some((p) => file.startsWith(p)) &&
  !ignoredFiles.has(file) &&
  !ignoredSuffixes.some((s) => file.endsWith(s));

/** The counterpart of a page in another locale, as a repository path. */
const counterpart = (page: string, locale: string) =>
  page.replace(/^docs\/src\/content\/docs\/[a-z-]+\//, `docs/src/content/docs/${locale}/`);

const section = (page: string) => page.replace(/^docs\/src\/content\/docs\/[a-z-]+\//, "").split("/")[0];

export type Finding = {
  kind: "uncovered" | "undocumented" | "untranslated";
  file: string;
  pages?: string[];
  detail?: string;
};

export const check = (changed: string[], pages: Page[]): Finding[] => {
  const changedSet = new Set(changed);
  const findings: Finding[] = [];

  for (const file of changed.filter(needsDocs)) {
    const covering = pages.filter((p) => p.sources.some((s) => covers(s, file)));
    if (covering.length === 0) {
      findings.push({ kind: "uncovered", file });
      continue;
    }
    const touched = covering.some((p) => locales.some((l) => changedSet.has(counterpart(p.path, l))));
    if (!touched) findings.push({ kind: "undocumented", file, pages: covering.map((p) => p.path) });
  }

  for (const file of changed.filter((f) => f.startsWith("docs/src/content/docs/") && /\.(md|mdx)$/.test(f))) {
    if (!bilingualSections.includes(section(file))) continue;
    for (const locale of locales) {
      const other = counterpart(file, locale);
      if (other !== file && !changedSet.has(other)) {
        findings.push({ kind: "untranslated", file, detail: other });
      }
    }
  }
  return findings;
};

const main = async () => {
  const args = process.argv.slice(2);
  const option = (name: string) => {
    const i = args.indexOf(name);
    return i >= 0 ? args[i + 1] : undefined;
  };
  const pages = loadPages();

  if (args.includes("--list")) {
    for (const p of pages) console.log(`${p.path}\n${p.sources.map((s) => `  - ${s}`).join("\n") || "  (no sources)"}`);
    return 0;
  }

  const head = option("--head") ?? "HEAD";
  const base =
    option("--base") ??
    ((await $`git -C ${repositoryRoot} rev-parse --verify -q origin/main`.quiet().nothrow()).exitCode === 0
      ? "origin/main"
      : "main");
  const mergeBase = (await $`git -C ${repositoryRoot} merge-base ${base} ${head}`.text()).trim();
  const changed = (await $`git -C ${repositoryRoot} diff --name-only ${mergeBase} ${head}`.text())
    .trim()
    .split("\n")
    .filter(Boolean);
  const messages = await $`git -C ${repositoryRoot} log --format=%B ${mergeBase}..${head}`.text();
  const waiver = messages.match(/^Docs-Impact:\s*none\s*(.*)$/m);

  if (changed.length === 0) {
    console.log("docs coverage: nothing changed");
    return 0;
  }

  const findings = check(changed, pages);
  const blocking = findings.filter((f) => f.kind !== "uncovered");
  const uncovered = findings.filter((f) => f.kind === "uncovered");

  for (const f of uncovered)
    console.log(
      `::notice::${f.file} is covered by no documentation page (add it to a page's \`sources\` if it should be)`,
    );

  if (blocking.length === 0) {
    console.log(`docs coverage: ok (${changed.length} changed files)`);
    return 0;
  }

  if (waiver) {
    const reason = waiver[1].replace(/^[\s—–:-]+/, "").trim();
    if (!reason) {
      console.error("::error::Docs-Impact: none needs a reason after it, e.g. `Docs-Impact: none — pure refactor`");
      return 1;
    }
    console.log(`docs coverage: waived by trailer — ${reason}`);
    for (const f of blocking) console.log(`  waived: ${f.kind} ${f.file}`);
    return 0;
  }

  console.error(`docs coverage: ${blocking.length} problem(s)\n`);
  for (const f of blocking) {
    if (f.kind === "undocumented") {
      console.error(`::error::${f.file} changed but none of the pages documenting it did:\n  ${f.pages?.join("\n  ")}`);
    } else {
      console.error(
        `::error::${f.file} changed without its counterpart ${f.detail} (user/ and admin/ pages are kept in both languages)`,
      );
    }
  }
  console.error(
    "\nUpdate the pages that describe this change, in both languages for user/ and admin/, or state why nothing documented changed with a commit trailer:\n  Docs-Impact: none — <reason>",
  );
  return 1;
};

if (import.meta.main) process.exit(await main());
