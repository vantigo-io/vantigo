// The breaking-change check's second half (API access design K31, K37, K38).
// CI runs `oasdiff breaking base.json head.json --fail-on ERR` on the
// integrator's documents of a pull request and its base; when it finds
// something, this script reads oasdiff's findings and the pull request's
// commits. Every operation with a breaking finding needs a commit marked with
// Conventional Commits' `!` whose message carries the trailer
//
//   API-Breaking: <operationId> — <reason>
//
// It only judges: it writes the verdict — `declared` when every operation
// has one, `undeclared` otherwise — to --output (the step's $GITHUB_OUTPUT)
// and fails on `undeclared`. A separate job, which runs nothing from the pull
// request, turns the verdict into the `breaking-api` label that
// .github/release.yml lists under "Breaking API changes".
//
//   bun run tools/api/breaking-check.ts <findings.json> <base>..<head> [--output <file>]
//
// findings.json is `oasdiff breaking … --format json`; the range names the
// pull request's commits, which must be fetched.

import { appendFile, readFile } from "node:fs/promises";

/** One oasdiff finding (`--format json`); level 3 is ERR. */
export type Finding = {
  id: string;
  text?: string;
  level: number;
  operation?: string;
  operationId?: string;
  path?: string;
};

export type Commit = { sha: string; subject: string; body: string };

export const ERR = 3;

/** The name a finding's operation is declared by: its operationId, else METHOD path. */
export function operationOf(finding: Finding): string {
  if (finding.operationId) return finding.operationId;
  return [finding.operation, finding.path].filter(Boolean).join(" ") || finding.id;
}

const BANG = /^[a-z]+(\([^)]*\))?!: \S/;
const TRAILER = /^API-Breaking:\s*(\S+)\s+(?:—|–|--|-)\s+(\S.*)$/;

/** The operations a commit declares breaking: none unless its subject has `!:`. */
export function declared(commit: Commit): string[] {
  if (!BANG.test(commit.subject)) return [];
  const out: string[] = [];
  for (const line of commit.body.split("\n")) {
    const match = TRAILER.exec(line.trim());
    if (match?.[1]) out.push(match[1]);
  }
  return out;
}

export type Verdict = { breaking: string[]; undeclared: string[] };

/** Which breaking operations no commit declares. */
export function judge(findings: Finding[], commits: Commit[]): Verdict {
  const breaking = [...new Set(findings.filter((f) => f.level >= ERR).map(operationOf))].sort();
  const covered = new Set(commits.flatMap(declared));
  return { breaking, undeclared: breaking.filter((op) => !covered.has(op)) };
}

const RECORD = "\x1e";
const FIELD = "\x00";

/** The format parseGitLog reads: hash, subject and body, NUL-separated, one record per commit. */
export const GIT_LOG_FORMAT = "%H%x00%s%x00%b%x1e";

/** Parses `git log --format=GIT_LOG_FORMAT`. */
export function parseGitLog(out: string): Commit[] {
  return out
    .split(RECORD)
    .map((entry) => entry.replace(/^\n/, ""))
    .filter((entry) => entry.trim() !== "")
    .map((entry) => {
      const [sha = "", subject = "", body = ""] = entry.split(FIELD);
      return { sha, subject, body };
    });
}

async function gitLog(range: string): Promise<Commit[]> {
  const proc = Bun.spawn(["git", "log", `--format=${GIT_LOG_FORMAT}`, range], { stdout: "pipe", stderr: "inherit" });
  const out = await new Response(proc.stdout).text();
  if ((await proc.exited) !== 0) throw new Error(`git log ${range} failed`);
  return parseGitLog(out);
}

async function main(argv: string[]): Promise<number> {
  const [findingsPath, range] = argv;
  const outputAt = argv.indexOf("--output");
  const output = outputAt >= 0 ? argv[outputAt + 1] : undefined;
  if (!findingsPath || !range?.includes("..")) {
    console.error("usage: breaking-check.ts <findings.json> <base>..<head> [--output <file>]");
    return 2;
  }
  const findings = JSON.parse(await readFile(findingsPath, "utf8")) as Finding[];
  const verdict = judge(findings, await gitLog(range));
  const outcome = verdict.breaking.length === 0 ? "clean" : verdict.undeclared.length === 0 ? "declared" : "undeclared";
  if (output) await appendFile(output, `verdict=${outcome}\n`);
  for (const op of verdict.undeclared) {
    console.log(
      `::error::${op} changes in a breaking way. Ship it as a new operation or version instead (reference/api-compatibility), or mark a commit with "!" and the trailer "API-Breaking: ${op} — <reason>".`,
    );
  }
  if (outcome === "declared") console.log(`Declared breaking API changes: ${verdict.breaking.join(", ")}`);
  if (outcome === "clean") console.log("No breaking change to the integrator's API.");
  return outcome === "undeclared" ? 1 : 0;
}

if (import.meta.main) {
  process.exit(await main(Bun.argv.slice(2)));
}
