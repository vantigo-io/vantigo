#!/usr/bin/env bun
import { existsSync, readdirSync, readFileSync } from "node:fs";
import { join, relative, resolve } from "node:path";
/**
 * Colour lint. Flags hard-coded colours that only work in the light colour
 * scheme, unless they sit inside a `light-dark(...)` call:
 *
 *  - hex colours and rgb()/rgba()/hsl()/hsla()/oklch()/oklab()/lab()/lch()/hwb()
 *    literals (not `color-mix()`, not `href="#anchor"`, not `#id {` selectors);
 *  - Mantine props that pin a light shade, plain or braced (ternaries and
 *    template literals included): `bg`/`bgc` = white or gray.0-2, `c`/`color` =
 *    dark or black;
 *  - `color`, `background`, `background-color` and `border*` declarations set to
 *    white or black, in CSS and in style objects;
 *  - `var(--mantine-color-gray-[0-2])`, `var(--mantine-color-white)` and the
 *    `vantigo-0` background.
 *
 * Exceptions live in tools/frontend/colour-allowlist.json (path, optional line
 * pattern, mandatory reason) or on the line itself, or the line above, as
 * `colour-lint-ignore: <reason>`. An allowlist entry that suppresses nothing
 * fails the check, so the list cannot rot.
 *
 * Usage: bun run tools/frontend/check-colours.ts
 */

export type Hit = { file: string; line: number; text: string; rule: string };
export type AllowEntry = { path: string; pattern?: string; reason: string };
export type CompiledAllow = AllowEntry & { re?: RegExp; used: number };

const repositoryRoot = resolve(import.meta.dir, "../..");
const allowlistPath = join(repositoryRoot, "tools/frontend/colour-allowlist.json");

/** A prop value: a braced expression (one level of nesting), or a quoted string. */
const value = String.raw`(?:\{(?:[^{}]|\{[^{}]*\})*\}|"[^"\n]*"|'[^'\n]*')`;
const notWord = String.raw`(?<![-\w])`;
const bareColour = String.raw`(?<![-\w])(?:white|black)(?![-\w])`;
const declaration = `(?:color|background(?:-color)?|backgroundColor|border(?:-(?:top|right|bottom|left))?(?:-color)?|border(?:Top|Right|Bottom|Left)?(?:Color)?)`;

type Rule = { rule: string; re: RegExp; inner?: RegExp };

const rules: Rule[] = [
  {
    rule: "hex colour",
    re: /#(?:[0-9a-fA-F]{8}|[0-9a-fA-F]{6}|[0-9a-fA-F]{3,4})(?![0-9a-zA-Z_-])/g,
  },
  { rule: "rgb/hsl/lab literal", re: new RegExp(`${notWord}(?:rgba?|hsla?|oklch|oklab|lab|lch|hwb)\\(`, "gi") },
  {
    rule: "light-only Mantine prop",
    re: new RegExp(`${notWord}(?:bg|bgc)\\s*[=:]\\s*${value}`, "g"),
    inner: /(?<=["'`])(?:white|gray\.[0-2])(?=["'`])/g,
  },
  {
    rule: "light-only Mantine prop",
    re: new RegExp(`${notWord}c\\s*[=:]\\s*${value}`, "g"),
    inner: /(?<=["'`])(?:black|dark)(?=["'`])/g,
  },
  {
    rule: "light-only Mantine prop",
    re: new RegExp(`${notWord}color\\s*=\\s*${value}`, "g"),
    inner: /(?<=["'`])(?:black|dark)(?=["'`])/g,
  },
  {
    rule: "white/black declaration",
    re: new RegExp(`${notWord}${declaration}\\s*:\\s*["'\`]?[^;}\\n"'\`]*?${bareColour}`, "g"),
    inner: new RegExp(bareColour, "g"),
  },
  { rule: "light-only Mantine variable", re: /var\(--mantine-color-(?:gray-[0-2]|white)\)/g },
  { rule: "vantigo-0 background", re: /--mantine-color-vantigo-0\b|\bvantigo\.0\b/g },
];

const ignoreMarker = /colour-lint-ignore:\s*\S/;
const skipFile = /(?:\.test\.tsx?|\.spec\.tsx?|\.gen\.ts|api-schema\.d\.ts)$/;
const skipDir = new Set(["node_modules", "dist", "__tests__", "test", "coverage"]);
const hrefBefore = /href\s*[=:]\s*\{?\s*["'`]$/;
const idSelectorAfter = /^\s*\{/;

/** The [start, end) ranges of every `light-dark(...)` call; an unclosed call has none. */
const lightDarkRanges = (text: string): [number, number][] => {
  const ranges: [number, number][] = [];
  for (const m of text.matchAll(/light-dark\(/g)) {
    const start = m.index ?? 0;
    let depth = 0;
    for (let i = start + "light-dark".length; i < text.length; i++) {
      if (text[i] === "(") depth++;
      else if (text[i] === ")" && --depth === 0) {
        ranges.push([start, i + 1]);
        break;
      }
    }
  }
  return ranges;
};

/** Compile the allowlist; a pattern that is not a regular expression names its entry. */
export const compileAllowlist = (entries: AllowEntry[]): CompiledAllow[] =>
  entries.map((entry, index) => {
    if (!entry.path || !entry.reason?.trim()) {
      throw new Error(`allowlist entry #${index + 1} (${entry.path ?? "no path"}): path and reason are required`);
    }
    if (entry.pattern === undefined) return { ...entry, used: 0 };
    try {
      return { ...entry, re: new RegExp(entry.pattern), used: 0 };
    } catch (error) {
      throw new Error(
        `allowlist entry #${index + 1} (${entry.path}): invalid pattern ${JSON.stringify(entry.pattern)}: ${(error as Error).message}`,
      );
    }
  });

const covers = (entry: CompiledAllow, file: string) =>
  entry.path === file || (entry.path.endsWith("/") && file.startsWith(entry.path));

export const scanText = (file: string, text: string, allow: CompiledAllow[] = []): Hit[] => {
  const ranges = lightDarkRanges(text);
  const lineStarts = [0];
  for (let i = 0; i < text.length; i++) if (text[i] === "\n") lineStarts.push(i + 1);
  const lines = text.split("\n");
  const fileAllow = allow.filter((a) => covers(a, file));
  const seen = new Set<number>();
  const hits: Hit[] = [];

  const consider = (at: number, rule: string) => {
    if (seen.has(at)) return;
    seen.add(at);
    if (ranges.some(([s, e]) => at >= s && at < e)) return;
    let lo = 0;
    let hi = lineStarts.length - 1;
    while (lo < hi) {
      const mid = (lo + hi + 1) >> 1;
      if (lineStarts[mid] <= at) lo = mid;
      else hi = mid - 1;
    }
    const lineText = lines[lo];
    if (ignoreMarker.test(lineText) || (lo > 0 && ignoreMarker.test(lines[lo - 1]))) return;
    const entry = fileAllow.find((a) => !a.re || a.re.test(lineText));
    if (entry) {
      entry.used++;
      return;
    }
    hits.push({ file, line: lo + 1, text: lineText.trim(), rule });
  };

  for (const { rule, re, inner } of rules) {
    for (const m of text.matchAll(re)) {
      const at = m.index ?? 0;
      if (rule === "hex colour") {
        if (hrefBefore.test(text.slice(Math.max(0, at - 24), at))) continue;
        if (idSelectorAfter.test(text.slice(at + m[0].length, at + m[0].length + 8))) continue;
      }
      if (!inner) {
        consider(at, rule);
        continue;
      }
      for (const im of m[0].matchAll(inner)) consider(at + (im.index ?? 0), rule);
    }
  }
  return hits.sort((a, b) => a.line - b.line);
};

const walk = (dir: string, out: string[]) => {
  for (const entry of readdirSync(dir, { withFileTypes: true })) {
    if (entry.isDirectory()) {
      if (!skipDir.has(entry.name)) walk(join(dir, entry.name), out);
    } else if (/\.(?:ts|tsx|css)$/.test(entry.name) && !skipFile.test(entry.name)) {
      out.push(join(dir, entry.name));
    }
  }
};

export const sourceFiles = (root: string): string[] => {
  const files: string[] = [];
  for (const group of ["apps", "packages"]) {
    let names: string[] = [];
    try {
      names = readdirSync(join(root, group));
    } catch {
      continue;
    }
    for (const name of names) {
      const src = group === "apps" ? join(root, group, name, "frontend/src") : join(root, group, name, "src");
      try {
        walk(src, files);
      } catch {
        // no such source directory in this workspace
      }
    }
  }
  return files.sort();
};

export const scanRepository = (root: string, allow: CompiledAllow[]): Hit[] =>
  sourceFiles(root).flatMap((f) => scanText(relative(root, f), readFileSync(f, "utf8"), allow));

/** Entries that point at a missing path, or that suppressed nothing in the scan. */
export const staleEntries = (root: string, allow: CompiledAllow[]): string[] =>
  allow.flatMap((a) => {
    const label = `${a.path}${a.pattern ? ` /${a.pattern}/` : ""}`;
    if (!existsSync(join(root, a.path))) return [`${label}: the path does not exist`];
    if (a.used === 0) return [`${label}: suppresses nothing; remove it or fix its pattern`];
    return [];
  });

if (import.meta.main) {
  let allow: CompiledAllow[];
  try {
    allow = compileAllowlist(JSON.parse(readFileSync(allowlistPath, "utf8")));
  } catch (error) {
    console.error(`check-colours: ${(error as Error).message}`);
    process.exit(1);
  }
  const hits = scanRepository(repositoryRoot, allow);
  const stale = staleEntries(repositoryRoot, allow);
  if (stale.length > 0) {
    for (const s of stale) console.error(`tools/frontend/colour-allowlist.json: ${s}`);
  }
  if (hits.length > 0) {
    for (const h of hits) console.error(`${h.file}:${h.line}  [${h.rule}]  ${h.text}`);
    console.error(
      `\ncheck-colours: ${hits.length} hard-coded light-only colour(s). Use a Mantine colour prop, a Mantine variable or a\n` +
        "--vantigo-* token, or wrap the pair in light-dark(light, dark). See 'Colour scheme and tokens' in\n" +
        "docs/src/content/docs/en/contributing/frontend-architecture.md. A legitimate exception goes in\n" +
        "tools/frontend/colour-allowlist.json or on the line as `colour-lint-ignore: <reason>`.",
    );
  }
  if (hits.length > 0 || stale.length > 0) process.exit(1);
  console.log("check-colours: no hard-coded light-only colours.");
}
