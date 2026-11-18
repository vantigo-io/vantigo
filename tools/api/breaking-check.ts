// The breaking-change check's second half (API access design K31, K37, K38).
// CI runs `oasdiff breaking base.json head.json --fail-on ERR` on the
// integrator's documents of a pull request and its base; when it finds
// something, this script reads oasdiff's findings and the pull request's
// commits. Every operation with a breaking finding needs a commit marked with
// Conventional Commits' `!` whose message carries the trailer
//
//   API-Breaking: <operationId> — <reason>
//
// When every one has it, the change is declared: the script adds the
// `breaking-api` label, which .github/release.yml turns into the release
// notes' "Breaking API changes". Otherwise it names what is missing and fails.
//
//   bun run tools/api/breaking-check.ts <findings.json> <commits.json> [--label <pr-number>]
//
// findings.json is `oasdiff breaking … --format json`; commits.json is
// `gh pr view <n> --json commits --jq .commits`.

import { readFile } from "node:fs/promises";

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
export const LABEL = "breaking-api";

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

/** The pull request's commits, from `gh pr view <n> --json commits --jq .commits`. */
export function commitsFrom(json: Array<{ oid?: string; messageHeadline?: string; messageBody?: string }>): Commit[] {
  return json.map((c) => ({ sha: c.oid ?? "", subject: c.messageHeadline ?? "", body: c.messageBody ?? "" }));
}

async function main(argv: string[]): Promise<number> {
  const [findingsPath, commitsPath] = argv;
  const labelAt = argv.indexOf("--label");
  const pr = labelAt >= 0 ? argv[labelAt + 1] : undefined;
  if (!findingsPath || !commitsPath) {
    console.error("usage: breaking-check.ts <findings.json> <commits.json> [--label <pr-number>]");
    return 2;
  }
  const findings = JSON.parse(await readFile(findingsPath, "utf8")) as Finding[];
  const commits = commitsFrom(JSON.parse(await readFile(commitsPath, "utf8")));
  const verdict = judge(findings, commits);
  if (verdict.breaking.length === 0) {
    console.log("No breaking change to the integrator's API.");
    return 0;
  }
  if (verdict.undeclared.length > 0) {
    for (const op of verdict.undeclared) {
      console.log(
        `::error::${op} changes in a breaking way. Ship it as a new operation or version instead (reference/api-compatibility), or mark a commit with "!" and the trailer "API-Breaking: ${op} — <reason>".`,
      );
    }
    return 1;
  }
  console.log(`Declared breaking API changes: ${verdict.breaking.join(", ")}`);
  if (pr) {
    const proc = Bun.spawn(["gh", "pr", "edit", pr, "--add-label", LABEL], { stdout: "inherit", stderr: "inherit" });
    if ((await proc.exited) !== 0) {
      console.log(`::error::Could not add the ${LABEL} label to #${pr}.`);
      return 1;
    }
  }
  return 0;
}

if (import.meta.main) {
  process.exit(await main(Bun.argv.slice(2)));
}
