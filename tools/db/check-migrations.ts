#!/usr/bin/env bun
/**
 * Schema compatibility check (Kubernetes readiness design, KR36–KR38).
 *
 * A release's migration runs once, and the previous release's pods keep
 * serving on the new schema until the rolling update replaces them. So the
 * schema a release migrates to must work with the previous release's binary:
 * a change that removes or narrows is split across two releases, expand first
 * and contract in the next.
 *
 * Given a git range, this script reads the migrations the range adds or edits under
 * apps/server/internal/db/migrations and fails on the statements an old
 * binary can trip over:
 *
 *   DROP TABLE, DROP SCHEMA, DROP VIEW/FUNCTION/TYPE/DOMAIN/SEQUENCE,
 *   ALTER TABLE … DROP [COLUMN], any RENAME, SET SCHEMA, ADD GENERATED ALWAYS,
 *   ALTER COLUMN … TYPE, SET NOT NULL, DROP DEFAULT,
 *   ADD COLUMN … NOT NULL without a DEFAULT,
 *   and, on a table that already exists, a new CHECK, UNIQUE, PRIMARY KEY,
 *   EXCLUDE or FOREIGN KEY constraint, a new unique index or a new trigger.
 *
 * Only goose's `-- +goose Up` section counts. A table the range itself creates
 * is new: what is done to it in the same range is not checked. A file opts out
 * with a line
 *
 *   -- vantigo:contract <reason>
 *
 * which a reviewer checks against the previous release: the contract step of
 * a change whose expand step has shipped. Migrations the range leaves alone
 * are never read. A few statements are warnings for the reviewer instead:
 * a replaced CHECK, a dropped index or constraint, and EXECUTE in a DO block.
 *
 * Usage:
 *   bun run tools/db/check-migrations.ts [--base <ref>] [--head <ref>]
 *   bun run tools/db/check-migrations.ts --files <path>…   # check these files
 *
 * Defaults: --base origin/main (or main), --head HEAD; the range starts at
 * their merge base.
 */
import { readFileSync } from "node:fs";
import { relative, resolve } from "node:path";
import { $ } from "bun";

const repositoryRoot = resolve(import.meta.dir, "../..");
export const migrationsDir = "apps/server/internal/db/migrations";

export type Finding = { file: string; line: number; rule: string; level: "error" | "warning"; statement: string };
export type Migration = { path: string; text: string };

const markerPattern = /^[ \t]*--[ \t]*vantigo:contract\b[ \t]*(.*)$/m;

/** The reason a file gives for opting out, `""` for a marker without one, or undefined. */
export const contractReason = (text: string): string | undefined => {
  const match = upSection(text).match(markerPattern);
  return match ? match[1].trim() : undefined;
};

/**
 * The goose Up section, with everything else blanked to spaces so offsets and
 * line numbers stay those of the file. A file without annotations is all Up.
 */
export const upSection = (text: string): string => {
  const lines = text.split("\n");
  if (!lines.some((l) => /^--\s*\+goose\s+(Up|Down)\b/.test(l))) return text;
  let up = false;
  return lines
    .map((line) => {
      const marker = line.match(/^--\s*\+goose\s+(Up|Down)\b/);
      if (marker) up = marker[1] === "Up";
      return up && !marker ? line : " ".repeat(line.length);
    })
    .join("\n");
};

type Span = { start: number; end: number; tag: string };

/**
 * Blanks comments, string literals, quoted identifiers' quotes and
 * dollar-quoted bodies to spaces, and returns the dollar-quoted spans so a DO
 * block's body can be read in its turn.
 */
export const mask = (sql: string): { masked: string; dollars: Span[] } => {
  const out = sql.split("");
  const dollars: Span[] = [];
  const blank = (from: number, to: number) => {
    for (let k = from; k < to; k++) if (out[k] !== "\n") out[k] = " ";
  };
  let i = 0;
  while (i < sql.length) {
    const c = sql[i];
    const next = sql[i + 1];
    if (c === "-" && next === "-") {
      const end = sql.indexOf("\n", i);
      const stop = end < 0 ? sql.length : end;
      blank(i, stop);
      i = stop;
    } else if (c === "/" && next === "*") {
      const end = sql.indexOf("*/", i + 2);
      const stop = end < 0 ? sql.length : end + 2;
      blank(i, stop);
      i = stop;
    } else if (c === "'") {
      // E'…' strings escape with a backslash as well as by doubling.
      const escaped = /[Ee]/.test(sql[i - 1] ?? "") && !/[\w$]/.test(sql[i - 2] ?? "");
      let j = i + 1;
      while (j < sql.length) {
        if (escaped && sql[j] === "\\") j += 2;
        else if (sql[j] === "'" && sql[j + 1] === "'") j += 2;
        else if (sql[j] === "'") break;
        else j++;
      }
      blank(i, j + 1);
      i = j + 1;
    } else if (c === '"') {
      const end = sql.indexOf('"', i + 1);
      const stop = end < 0 ? sql.length : end;
      out[i] = " ";
      if (end >= 0) out[end] = " ";
      // What a quoted name holds must not split the name or the statement.
      for (let k = i + 1; k < stop; k++) if (!/[\w$]/.test(sql[k])) out[k] = "_";
      i = stop + 1;
    } else if (c === "$") {
      const tag = sql.slice(i).match(/^\$([A-Za-z_][A-Za-z0-9_]*)?\$/);
      if (tag && !/[A-Za-z0-9_]/.test(sql[i - 1] ?? "")) {
        const open = tag[0];
        const close = sql.indexOf(open, i + open.length);
        const stop = close < 0 ? sql.length : close + open.length;
        dollars.push({ start: i + open.length, end: close < 0 ? sql.length : close, tag: open });
        blank(i, stop);
        i = stop;
      } else i++;
    } else i++;
  }
  return { masked: out.join(""), dollars };
};

/** A statement, its offset, and where its slice of the file ends (the semicolon). */
type Statement = { text: string; offset: number; end: number };

const statements = (masked: string): Statement[] => {
  const result: Statement[] = [];
  let start = 0;
  for (let i = 0; i <= masked.length; i++) {
    if (i === masked.length || masked[i] === ";") {
      const raw = masked.slice(start, i);
      const lead = raw.length - raw.trimStart().length;
      if (raw.trim()) result.push({ text: raw.trim(), offset: start + lead, end: i });
      start = i + 1;
    }
  }
  return result;
};

/** Splits on commas outside parentheses: an ALTER TABLE's actions. */
const topLevelSplit = (s: string): string[] => {
  const parts: string[] = [];
  let depth = 0;
  let from = 0;
  for (let i = 0; i < s.length; i++) {
    if (s[i] === "(") depth++;
    else if (s[i] === ")") depth--;
    else if (s[i] === "," && depth === 0) {
      parts.push(s.slice(from, i));
      from = i + 1;
    }
  }
  parts.push(s.slice(from));
  return parts.map((p) => p.trim()).filter(Boolean);
};

const name = String.raw`([A-Za-z_][\w$]*(?:\s*\.\s*[A-Za-z_][\w$]*)*)`;
const normalise = (n: string) => n.replace(/\s+/g, "").toLowerCase();

const createTable = new RegExp(
  String.raw`^CREATE\s+(?:(?:GLOBAL|LOCAL)\s+)?(?:(?:TEMPORARY|TEMP|UNLOGGED)\s+)?TABLE\s+(?:IF\s+NOT\s+EXISTS\s+)?${name}`,
  "i",
);
const alterTable = new RegExp(
  String.raw`^ALTER\s+TABLE\s+(?:IF\s+EXISTS\s+)?(?:ONLY\s+)?${name}\s*\*?\s+([\s\S]*)$`,
  "i",
);
const dropTable = /^DROP\s+TABLE\s+(?:IF\s+EXISTS\s+)?([\s\S]*?)(?:\s+(?:CASCADE|RESTRICT))?$/i;
const createUniqueIndex = new RegExp(String.raw`^CREATE\s+UNIQUE\s+INDEX\b[\s\S]*?\bON\s+(?:ONLY\s+)?${name}`, "i");
const createTrigger = new RegExp(
  String.raw`^CREATE\s+(?:OR\s+REPLACE\s+)?(?:CONSTRAINT\s+)?TRIGGER\b[\s\S]*?\bON\s+${name}`,
  "i",
);

/**
 * What a range adds: the tables it creates and, per table, the columns it adds
 * and the constraints it drops.
 */
export type Additions = {
  tables: Set<string>;
  columns: Map<string, Set<string>>;
  dropped: Map<string, Set<string>>;
  /** The constraints and indexes the range creates, by name. */
  named: Set<string>;
};

/** An object's own name, without its schema. */
const lastName = (n: string) => normalise(n).split(".").pop() ?? "";

const remember = (map: Map<string, Set<string>>, table: string, value: string) => {
  if (!map.has(table)) map.set(table, new Set());
  map.get(table)?.add(value.toLowerCase());
};

// The IF [NOT] EXISTS forms may name what does not exist, or already does:
// neither counts as dropped or added.
const dropConstraint = /^DROP\s+CONSTRAINT\s+(?!IF\s+EXISTS\b)([A-Za-z_][\w$]*)/i;
const addColumn = /^ADD\s+(?:COLUMN\s+)?(?!IF\s+NOT\s+EXISTS\b)([A-Za-z_][\w$]*)/i;

/** The tables a set of migrations creates in their Up sections, and the columns they add. */
export const additions = (migrations: Migration[]): Additions => {
  const result: Additions = { tables: new Set(), columns: new Map(), dropped: new Map(), named: new Set() };
  for (const m of migrations) {
    for (const s of statements(mask(upSection(m.text)).masked)) {
      // CREATE TABLE IF NOT EXISTS may name a table that exists: not new.
      const created = s.text.match(createTable);
      if (created && !/\bIF\s+NOT\s+EXISTS\b/i.test(created[0])) result.tables.add(normalise(created[1]));
      const index = s.text.match(
        /^CREATE\s+(?:UNIQUE\s+)?INDEX\s+(?:CONCURRENTLY\s+)?(?:IF\s+NOT\s+EXISTS\s+)?([A-Za-z_][\w$]*(?:\s*\.\s*[A-Za-z_][\w$]*)?)\s+ON\b/i,
      );
      if (index) result.named.add(lastName(index[1]));
      const alter = s.text.match(alterTable);
      if (!alter) continue;
      for (const c of alter[2].matchAll(
        /\bCONSTRAINT\s+([A-Za-z_][\w$]*)\s+(?:CHECK|UNIQUE|PRIMARY|EXCLUDE|FOREIGN)\b/gi,
      )) {
        result.named.add(c[1].toLowerCase());
      }
      const table = normalise(alter[1]);
      for (const action of topLevelSplit(alter[2])) {
        const dropped = action.match(dropConstraint);
        if (dropped) remember(result.dropped, table, dropped[1]);
        const column = action.match(addColumn);
        if (column && !/^(CONSTRAINT|CHECK|UNIQUE|PRIMARY|EXCLUDE|FOREIGN)$/i.test(column[1])) {
          remember(result.columns, table, column[1]);
        }
      }
    }
  }
  return result;
};

/** The text inside the parentheses that open at or after `from`. */
const parenthesised = (s: string, from: number): string | undefined => {
  const open = s.indexOf("(", from);
  if (open < 0) return undefined;
  let depth = 0;
  for (let i = open; i < s.length; i++) {
    if (s[i] === "(") depth++;
    else if (s[i] === ")" && --depth === 0) return s.slice(open + 1, i);
  }
  return undefined;
};

/** Words a CHECK expression uses that are not columns. */
const sqlWords = new Set(
  "and or not is null in true false between like ilike similar to any all some array case when then else end distinct from unknown".split(
    " ",
  ),
);

/** The columns an expression names: identifiers that are no keyword, cast type or function. */
const columnsOf = (expression: string): string[] =>
  [
    ...expression
      .replace(/::\s*[A-Za-z_][\w\s]*?(\(\s*\d+(\s*,\s*\d+)?\s*\))?(\[\])?(?=[\s),=<>!]|$)/g, " ")
      .matchAll(/([A-Za-z_][\w$]*)(\s*\()?/g),
  ]
    .filter((m) => !m[2] && !sqlWords.has(m[1].toLowerCase()))
    .map((m) => m[1].toLowerCase());

type Rule = { rule: string; level: "error" | "warning" };
const error = (rule: string): Rule => ({ rule, level: "error" });

/**
 * The rules one ALTER TABLE action on an existing table breaks. `added` holds
 * the columns the range adds to the table, `dropped` the constraints it drops
 * from it.
 */
const alterAction = (action: string, added: Set<string>, dropped: Set<string>, named: Set<string>): Rule[] => {
  const a = action.replace(/\s+/g, " ").toUpperCase();
  if (/^RENAME\b/.test(a)) return [error("RENAME")];
  // Dropping a constraint widens, but a unique one may be what an old
  // binary's ON CONFLICT resolves against.
  const droppedName = action.match(dropConstraint)?.[1].toLowerCase();
  if (/^DROP CONSTRAINT\b/.test(a)) {
    if (droppedName && named.has(droppedName)) return [];
    return [{ rule: "a dropped constraint, which an ON CONFLICT of the previous release may need", level: "warning" }];
  }
  if (/^DROP (EXPRESSION|IDENTITY)\b/.test(a)) return [];
  if (/^DROP\b/.test(a)) return [error("DROP COLUMN")];
  if (/^SET SCHEMA\b/.test(a)) return [error("SET SCHEMA")];
  const column = a.match(/^ALTER (?:COLUMN )?(?!COLUMN )\S+ (.*)$/)?.[1] ?? "";
  if (/^(SET DATA )?TYPE\b/.test(column)) return [error("ALTER COLUMN … TYPE")];
  if (/^SET NOT NULL\b/.test(column)) return [error("SET NOT NULL")];
  if (/^DROP DEFAULT\b/.test(column)) return [error("DROP DEFAULT")];
  if (/^ADD GENERATED ALWAYS\b/.test(column)) return [error("ADD GENERATED ALWAYS")];
  const constraint = a.match(/^ADD (?:CONSTRAINT (\S+) )?(CHECK|UNIQUE|PRIMARY KEY|EXCLUDE|FOREIGN KEY)\b/);
  if (constraint) {
    // A CHECK or foreign key on columns the range adds holds for an old
    // insert, which leaves them NULL or at their DEFAULT. A UNIQUE does not:
    // every old insert gets the same DEFAULT.
    if (constraint[2] === "CHECK" || constraint[2] === "FOREIGN KEY") {
      const columns = columnsOf(parenthesised(action, constraint[0].length - constraint[2].length) ?? "");
      if (columns.length > 0 && columns.every((c) => added.has(c))) return [];
    }
    // A CHECK that replaces one of the same name the range drops usually
    // widens a list of values; whether it does is the reviewer's to say.
    if (constraint[2] === "CHECK" && constraint[1] && dropped.has(constraint[1].toLowerCase())) {
      return [{ rule: "a replaced CHECK, which must only widen", level: "warning" }];
    }
    return [error("a new constraint on an existing table")];
  }
  if (/^ADD\b/.test(a)) {
    const column = a.replace(/^ADD (COLUMN )?(IF NOT EXISTS )?/, "");
    const defaulted =
      /\b(DEFAULT\b(?! NULL\b)|GENERATED\b)/.test(column) || /^\S+ (SMALL|BIG)?SERIAL\d?\b/.test(column);
    if (/\bNOT NULL\b/.test(column) && !defaulted) return [error("ADD COLUMN … NOT NULL without a DEFAULT")];
    // Every old insert gets the same DEFAULT, which a UNIQUE refuses the second time.
    if (/\bUNIQUE\b/.test(column) && defaulted) return [error("a new constraint on an existing table")];
  }
  return [];
};

/** The rules a statement breaks, given what the range adds. */
const statementRules = (s: string, range: Additions): Rule[] => {
  const isNew = (n: string) => range.tables.has(normalise(n));
  if (/^DROP\s+SCHEMA\b/i.test(s)) return [error("DROP SCHEMA")];
  const dropObject = s.match(/^DROP\s+(FUNCTION|PROCEDURE|(?:MATERIALIZED\s+)?VIEW|TYPE|DOMAIN|SEQUENCE)\b/i);
  if (dropObject) return [error(`DROP ${dropObject[1].replace(/\s+/g, " ").toUpperCase()}`)];
  const dropIndex = s.match(
    /^DROP\s+INDEX\s+(?:CONCURRENTLY\s+)?(?:IF\s+EXISTS\s+)?([\s\S]*?)(?:\s+(?:CASCADE|RESTRICT))?$/i,
  );
  if (dropIndex) {
    const names = dropIndex[1].split(",").map(lastName);
    if (names.every((n) => range.named.has(n))) return [];
    return [{ rule: "a dropped index, which an ON CONFLICT of the previous release may need", level: "warning" }];
  }
  const drop = s.match(dropTable);
  if (drop) {
    const tables = drop[1].split(",").map((t) => t.trim());
    return tables.every(isNew) ? [] : [error("DROP TABLE")];
  }
  const alter = s.match(alterTable);
  if (alter) {
    if (isNew(alter[1])) return [];
    const table = normalise(alter[1]);
    const added = range.columns.get(table) ?? new Set<string>();
    const dropped = range.dropped.get(table) ?? new Set<string>();
    const rules = topLevelSplit(alter[2]).flatMap((x) => alterAction(x, added, dropped, range.named));
    return rules.filter((r, i) => rules.findIndex((o) => o.rule === r.rule) === i);
  }
  if (/^ALTER\s+[\s\S]*\bRENAME\b/i.test(s)) return [error("RENAME")];
  const unique = s.match(createUniqueIndex);
  if (unique) return isNew(unique[1]) ? [] : [error("a new unique index on an existing table")];
  const trigger = s.match(createTrigger);
  if (trigger) return isNew(trigger[1]) ? [] : [error("a new trigger on an existing table")];
  return [];
};

/** PL/pgSQL control words a statement in a DO block's body may follow. */
const control =
  /^(?:<<\s*[A-Za-z_]\w*\s*>>|DECLARE\b[\s\S]*?\bBEGIN|BEGIN|EXCEPTION\s+WHEN\b[\s\S]*?\bTHEN|WHEN\b[\s\S]*?\bTHEN|END(?:\s+(?:IF|LOOP|CASE))?|ELSE|ELSIF\b[\s\S]*?\bTHEN|IF\b[\s\S]*?\bTHEN|(?:FOR|WHILE|FOREACH)\b[\s\S]*?\bLOOP|LOOP)\s+/i;

/** Strips the control words off a statement in a DO block's body. */
const plpgsql = (s: Statement): Statement => {
  let { text, offset } = s;
  for (let m = text.match(control); m; m = text.match(control)) {
    const rest = text.slice(m[0].length);
    offset += text.length - rest.length;
    text = rest;
  }
  return { ...s, text, offset };
};

const lineAt = (text: string, offset: number) => text.slice(0, offset).split("\n").length;

/** Every unsafe statement in one migration's Up section. */
export const analyse = (migration: Migration, range: Additions): Finding[] => {
  const up = upSection(migration.text);
  const findings: Finding[] = [];
  const walk = (sql: string, base: number) => {
    const { masked, dollars } = mask(sql);
    for (const s of statements(masked).map((s) => (base === 0 ? s : plpgsql(s)))) {
      for (const { rule, level } of statementRules(s.text, range)) {
        const original = sql.slice(s.offset, s.offset + s.text.length).replace(/\s+/g, " ");
        findings.push({
          file: migration.path,
          line: lineAt(migration.text, base + s.offset),
          rule,
          level,
          statement: original.length > 120 ? `${original.slice(0, 117)}…` : original,
        });
      }
      // A DO block runs its body now; a function's body runs later, against
      // whichever binary calls it, and is not read.
      if (base > 0 && /^EXECUTE\b/i.test(s.text)) {
        findings.push({
          file: migration.path,
          line: lineAt(migration.text, base + s.offset),
          rule: "dynamic SQL, which the check cannot read",
          level: "warning",
          statement: sql
            .slice(s.offset, s.offset + s.text.length)
            .replace(/\s+/g, " ")
            .slice(0, 120),
        });
      }
      if (/^DO\b/i.test(s.text)) {
        for (const d of dollars) {
          if (d.start >= s.offset && d.end <= s.end) {
            walk(sql.slice(d.start, d.end), base + d.start);
          }
        }
      }
    }
  };
  walk(up, 0);
  return findings;
};

export type Result = {
  /** The statements that fail the check. */
  errors: Finding[];
  /** The statements a reviewer must look at, which do not fail it. */
  warnings: Finding[];
  /** The files that opt out, with their reasons. */
  waived: { file: string; reason: string }[];
  /** The files whose marker gives no reason. */
  missingReason: string[];
};

/** Checks the migrations a range adds. */
export const check = (migrations: Migration[]): Result => {
  const range = additions(migrations);
  const result: Result = { errors: [], warnings: [], waived: [], missingReason: [] };
  for (const m of migrations) {
    const reason = contractReason(m.text);
    if (reason === "") {
      result.missingReason.push(m.path);
      continue;
    }
    const findings = analyse(m, range);
    if (findings.length === 0) continue;
    if (reason) result.waived.push({ file: m.path, reason });
    else {
      result.errors.push(...findings.filter((f) => f.level === "error"));
      result.warnings.push(...findings.filter((f) => f.level === "warning"));
    }
  }
  return result;
};

const addedMigrations = async (base: string, head: string): Promise<Migration[]> => {
  const mergeBase = (await $`git -C ${repositoryRoot} merge-base ${base} ${head}`.text()).trim();
  const paths = (
    await $`git -C ${repositoryRoot} diff --no-renames --diff-filter=AM --name-only ${mergeBase} ${head} -- ${migrationsDir}`.text()
  )
    .trim()
    .split("\n")
    .filter((p) => p.endsWith(".sql"));
  return Promise.all(
    paths.map(async (path) => ({ path, text: await $`git -C ${repositoryRoot} show ${`${head}:${path}`}`.text() })),
  );
};

const main = async () => {
  const args = process.argv.slice(2);
  const option = (flag: string) => {
    const i = args.indexOf(flag);
    return i >= 0 ? args[i + 1] : undefined;
  };

  let migrations: Migration[];
  const filesAt = args.indexOf("--files");
  if (filesAt >= 0) {
    migrations = args.slice(filesAt + 1).map((p) => ({
      path: relative(repositoryRoot, resolve(p)),
      text: readFileSync(p, "utf8"),
    }));
  } else {
    const head = option("--head") ?? "HEAD";
    const base =
      option("--base") ??
      ((await $`git -C ${repositoryRoot} rev-parse --verify -q origin/main`.quiet().nothrow()).exitCode === 0
        ? "origin/main"
        : "main");
    migrations = await addedMigrations(base, head);
  }

  if (migrations.length === 0) {
    console.log("migration compatibility: no migrations added");
    return 0;
  }

  const { errors, warnings, waived, missingReason } = check(migrations);
  for (const w of waived) console.log(`migration compatibility: ${w.file} is a contract step — ${w.reason}`);
  for (const file of missingReason) {
    console.error(
      `::error file=${file}::\`-- vantigo:contract\` needs a reason, e.g. \`-- vantigo:contract drops invoices.legacy_kid, unread since 2026.11\``,
    );
  }
  for (const f of warnings) {
    console.log(
      `::warning file=${f.file},line=${f.line}::${f.rule}: check that every value the previous release writes still passes: ${f.statement}`,
    );
  }
  for (const f of errors) {
    console.error(
      `::error file=${f.file},line=${f.line}::${f.rule} breaks the previous release's binary: ${f.statement}`,
    );
  }
  if (errors.length === 0 && missingReason.length === 0) {
    console.log(`migration compatibility: ok (${migrations.length} added migration(s))`);
    return 0;
  }
  console.error(
    "\nThe previous release's pods keep serving on this schema during a rolling upgrade. Split the change: expand in this release (add, write both, read the new), contract in the next with `-- vantigo:contract <reason>`.\nSee https://docs.vantigo.io/en/contributing/schema-changes/",
  );
  return 1;
};

if (import.meta.main) process.exit(await main());
