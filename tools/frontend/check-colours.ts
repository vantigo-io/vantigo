#!/usr/bin/env bun
import { readdirSync, readFileSync } from "node:fs";
import { join, relative, resolve } from "node:path";
/**
 * Colour lint. Flags hard-coded colours that only work in the light colour
 * scheme: hex, rgb()/rgba()/hsl() literals, Mantine props that pin a light
 * shade, and the light-only Mantine variables, unless they sit inside a
 * `light-dark(...)` call. Exceptions live in tools/frontend/colour-allowlist.json
 * or on the line itself (or the line above) as `colour-lint-ignore: <reason>`.
 *
 * Usage: bun run tools/frontend/check-colours.ts
 */

export type Hit = { file: string; line: number; text: string; rule: string };
export type AllowEntry = { path: string; pattern?: string; reason: string };

const repositoryRoot = resolve(import.meta.dir, "../..");
const allowlistPath = join(repositoryRoot, "tools/frontend/colour-allowlist.json");

const rules: { rule: string; re: RegExp }[] = [
  { rule: "hex colour", re: /#(?:[0-9a-fA-F]{8}|[0-9a-fA-F]{6}|[0-9a-fA-F]{3,4})(?![0-9a-zA-Z_-])/g },
  { rule: "rgb/hsl literal", re: /\b(?:rgba?|hsla?)\(/g },
  { rule: "light-only Mantine prop", re: /\bbg\s*[=:]\s*\{?\s*["'](?:white|gray\.[01])["']/g },
  { rule: "light-only Mantine prop", re: /\bc\s*[=:]\s*\{?\s*["'](?:black|dark)["']/g },
  { rule: "light-only Mantine variable", re: /var\(--mantine-color-(?:gray-[0-2]|white)\)/g },
  { rule: "vantigo-0 background", re: /--mantine-color-vantigo-0\b|\bvantigo\.0\b/g },
];

const ignoreMarker = /colour-lint-ignore:\s*\S/;
const skipFile = /(?:\.test\.tsx?|\.spec\.tsx?|\.gen\.ts|api-schema\.d\.ts)$/;
const skipDir = new Set(["node_modules", "dist", "__tests__", "test", "coverage"]);

/** The [start, end) ranges of every `light-dark(...)` call in the text. */
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

export const scanText = (file: string, text: string, allow: AllowEntry[] = []): Hit[] => {
  const ranges = lightDarkRanges(text);
  const lineStarts = [0];
  for (let i = 0; i < text.length; i++) if (text[i] === "\n") lineStarts.push(i + 1);
  const lines = text.split("\n");
  const fileAllow = allow.filter((a) => a.path === file || (a.path.endsWith("/") && file.startsWith(a.path)));
  const hits: Hit[] = [];
  for (const { rule, re } of rules) {
    for (const m of text.matchAll(re)) {
      const at = m.index ?? 0;
      if (ranges.some(([s, e]) => at >= s && at < e)) continue;
      let lo = 0;
      let hi = lineStarts.length - 1;
      while (lo < hi) {
        const mid = (lo + hi + 1) >> 1;
        if (lineStarts[mid] <= at) lo = mid;
        else hi = mid - 1;
      }
      const lineText = lines[lo];
      if (ignoreMarker.test(lineText) || (lo > 0 && ignoreMarker.test(lines[lo - 1]))) continue;
      if (fileAllow.some((a) => !a.pattern || new RegExp(a.pattern).test(lineText))) continue;
      hits.push({ file, line: lo + 1, text: lineText.trim(), rule });
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

export const scanRepository = (root: string, allow: AllowEntry[]): Hit[] =>
  sourceFiles(root).flatMap((f) => scanText(relative(root, f), readFileSync(f, "utf8"), allow));

if (import.meta.main) {
  const allow: AllowEntry[] = JSON.parse(readFileSync(allowlistPath, "utf8"));
  const hits = scanRepository(repositoryRoot, allow);
  if (hits.length === 0) {
    console.log("check-colours: no hard-coded light-only colours.");
  } else {
    for (const h of hits) console.error(`${h.file}:${h.line}  [${h.rule}]  ${h.text}`);
    console.error(
      `\ncheck-colours: ${hits.length} hard-coded light-only colour(s). Use a Mantine colour prop, a Mantine variable or a\n` +
        "--vantigo-* token, or wrap the pair in light-dark(light, dark). See 'Colour scheme and tokens' in\n" +
        "docs/src/content/docs/en/contributing/frontend-architecture.md. A legitimate exception goes in\n" +
        "tools/frontend/colour-allowlist.json or on the line as `colour-lint-ignore: <reason>`.",
    );
    process.exit(1);
  }
}
