import { describe, expect, test } from "bun:test";
import { type Additions, additions, analyse, check, contractReason, mask, upSection } from "./check-migrations";

const up = (sql: string) => `-- +goose Up\n${sql}\n\n-- +goose Down\nDROP TABLE app.whatever;\n`;
const none = (): Additions => ({ tables: new Set(), columns: new Map(), dropped: new Map(), named: new Set() });
/** The rules a statement breaks, with the range's additions read from the statement itself. */
const rules = (sql: string, created: string[] = []) => {
  const migration = { path: "m.sql", text: up(sql) };
  const range = additions([migration]);
  for (const t of created) range.tables.add(t);
  return analyse(migration, range).map((f) => (f.level === "warning" ? `warning: ${f.rule}` : f.rule));
};

describe("upSection", () => {
  test("keeps Up and blanks Down, preserving lines", () => {
    const text = "-- +goose Up\nCREATE TABLE a (x int);\n-- +goose Down\nDROP TABLE a;";
    const section = upSection(text);
    expect(section.split("\n")).toHaveLength(4);
    expect(section).toContain("CREATE TABLE a");
    expect(section).not.toContain("DROP");
  });
  test("a file without annotations is all Up", () => {
    expect(upSection("DROP TABLE a;")).toBe("DROP TABLE a;");
  });
});

describe("mask", () => {
  test("blanks comments, strings and dollar bodies", () => {
    const { masked } = mask("SELECT 'DROP TABLE x'; -- DROP TABLE y\n/* RENAME */ SELECT $f$ DROP TABLE z $f$;");
    expect(masked).not.toMatch(/DROP|RENAME/);
    expect(masked.length).toBe(
      "SELECT 'DROP TABLE x'; -- DROP TABLE y\n/* RENAME */ SELECT $f$ DROP TABLE z $f$;".length,
    );
  });
});

describe("the statements KR37 names", () => {
  test.each([
    ["DROP TABLE app.things", "DROP TABLE"],
    ["DROP TABLE IF EXISTS app.things CASCADE", "DROP TABLE"],
    ["DROP SCHEMA legacy CASCADE", "DROP SCHEMA"],
    ["ALTER TABLE app.things DROP COLUMN name", "DROP COLUMN"],
    ["ALTER TABLE app.things DROP name", "DROP COLUMN"],
    ["ALTER TABLE app.things RENAME COLUMN a TO b", "RENAME"],
    ["ALTER TABLE app.things RENAME TO stuff", "RENAME"],
    ["ALTER INDEX app.ix_things RENAME TO ix_stuff", "RENAME"],
    ["ALTER TYPE app.state RENAME VALUE 'a' TO 'b'", "RENAME"],
    ["ALTER TABLE app.things ALTER COLUMN n TYPE bigint", "ALTER COLUMN … TYPE"],
    ["ALTER TABLE app.things ALTER n SET DATA TYPE bigint", "ALTER COLUMN … TYPE"],
    ["ALTER TABLE app.things ALTER COLUMN n SET NOT NULL", "SET NOT NULL"],
    ["ALTER TABLE app.things ALTER COLUMN n DROP DEFAULT", "DROP DEFAULT"],
    ["ALTER TABLE app.things ADD COLUMN n int NOT NULL", "ADD COLUMN … NOT NULL without a DEFAULT"],
    ["ALTER TABLE app.things ADD n text NOT NULL REFERENCES app.other (id)", "ADD COLUMN … NOT NULL without a DEFAULT"],
    ["ALTER TABLE app.things ADD CONSTRAINT ck CHECK (n > 0)", "a new constraint on an existing table"],
    ["ALTER TABLE app.things ADD CONSTRAINT ck CHECK (n > 0) NOT VALID", "a new constraint on an existing table"],
    ["ALTER TABLE app.things ADD UNIQUE (a, b)", "a new constraint on an existing table"],
    [
      "ALTER TABLE app.things ADD CONSTRAINT fk FOREIGN KEY (o) REFERENCES app.other (id)",
      "a new constraint on an existing table",
    ],
    ["CREATE UNIQUE INDEX ux ON app.things (a)", "a new unique index on an existing table"],
    [
      "CREATE UNIQUE INDEX CONCURRENTLY IF NOT EXISTS ux ON ONLY app.things (a)",
      "a new unique index on an existing table",
    ],
    [
      "CREATE TRIGGER t BEFORE INSERT ON app.things FOR EACH ROW EXECUTE FUNCTION app.f()",
      "a new trigger on an existing table",
    ],
    [
      "CREATE OR REPLACE TRIGGER t AFTER UPDATE ON app.things FOR EACH ROW EXECUTE FUNCTION app.f()",
      "a new trigger on an existing table",
    ],
  ])("%s", (sql, rule) => {
    expect(rules(`${sql};`)).toEqual([rule]);
  });

  test("an ALTER TABLE with several actions reports each rule once", () => {
    expect(rules("ALTER TABLE app.things DROP COLUMN a, DROP COLUMN b, ALTER COLUMN c SET NOT NULL;")).toEqual([
      "DROP COLUMN",
      "SET NOT NULL",
    ]);
  });

  test("case, whitespace and quoting do not hide a statement", () => {
    expect(rules('alter   table\n  "app"."things"\n  drop\n  column  "name";')).toEqual(["DROP COLUMN"]);
  });
});

describe("what expands", () => {
  test.each([
    "CREATE TABLE app.things (id uuid PRIMARY KEY, n int NOT NULL CHECK (n > 0) UNIQUE)",
    "ALTER TABLE app.things ADD COLUMN n int",
    "ALTER TABLE app.things ADD COLUMN IF NOT EXISTS n int NOT NULL DEFAULT 0",
    "ALTER TABLE app.things ADD COLUMN n bigint NOT NULL GENERATED ALWAYS AS IDENTITY",
    "ALTER TABLE app.things ADD COLUMN o uuid REFERENCES app.other (id)",
    "ALTER TABLE app.things ALTER COLUMN n DROP NOT NULL",
    "ALTER TABLE app.things ALTER COLUMN n SET DEFAULT 0",
    "ALTER TABLE app.things VALIDATE CONSTRAINT ck",
    "CREATE INDEX ix ON app.things (n)",
    "CREATE OR REPLACE FUNCTION app.f() RETURNS trigger LANGUAGE plpgsql AS $$ BEGIN DROP TABLE app.x; RETURN NEW; END $$",
    "INSERT INTO app.things (name) VALUES ('DROP TABLE app.things; RENAME')",
    "UPDATE app.things SET n = 0 WHERE n IS NULL",
    "COMMENT ON COLUMN app.things.n IS 'was renamed'",
  ])("%s", (sql) => {
    expect(rules(`${sql};`)).toEqual([]);
  });
});

describe("what the review looks at", () => {
  test.each([
    [
      "ALTER TABLE app.things DROP CONSTRAINT uq_things_code",
      "a dropped constraint, which an ON CONFLICT of the previous release may need",
    ],
    ["DROP INDEX app.ux_things_code", "a dropped index, which an ON CONFLICT of the previous release may need"],
    [
      "DO $$ BEGIN EXECUTE format('ALTER TABLE %I DROP COLUMN c', 't'); END $$",
      "dynamic SQL, which the check cannot read",
    ],
  ])("%s", (sql, rule) => {
    expect(rules(`${sql};`)).toEqual([`warning: ${rule}`]);
  });

  test("a constraint or index the range re-creates is not", () => {
    expect(
      rules(
        "ALTER TABLE app.things DROP CONSTRAINT uq_code, ADD CONSTRAINT uq_code UNIQUE (code, tenant);\nDROP INDEX app.ix_code;\nCREATE INDEX ix_code ON app.things (code, n);",
      ).filter((r) => !r.includes("new constraint")),
    ).toEqual([]);
  });
});

describe("what the review found", () => {
  test.each([
    ["DROP FUNCTION app.f(text)", "DROP FUNCTION"],
    ["DROP VIEW app.v", "DROP VIEW"],
    ["DROP MATERIALIZED VIEW app.v", "DROP MATERIALIZED VIEW"],
    ["DROP TYPE app.state", "DROP TYPE"],
    ["DROP SEQUENCE app.s", "DROP SEQUENCE"],
    ["ALTER TABLE app.things SET SCHEMA archive", "SET SCHEMA"],
    ["ALTER TABLE app.things ALTER COLUMN id ADD GENERATED ALWAYS AS IDENTITY", "ADD GENERATED ALWAYS"],
    ['ALTER TABLE app."Order Items" DROP COLUMN c', "DROP COLUMN"],
    ['ALTER TABLE app."a;b" DROP COLUMN c', "DROP COLUMN"],
    ['ALTER TABLE app.things ALTER COLUMN "my col" TYPE bigint', "ALTER COLUMN … TYPE"],
    ["ALTER TABLE app.things ADD COLUMN n int NOT NULL DEFAULT NULL", "ADD COLUMN … NOT NULL without a DEFAULT"],
    ["ALTER TABLE app.things ADD COLUMN code text NOT NULL DEFAULT '' UNIQUE", "a new constraint on an existing table"],
    ["DO $$ <<blk>> BEGIN ALTER TABLE app.things DROP COLUMN c; END $$", "DROP COLUMN"],
  ])("%s", (sql, rule) => {
    expect(rules(`${sql};`)).toEqual([rule]);
  });

  test("an E'' string's backslash escape does not hide the next statement", () => {
    expect(
      rules("UPDATE app.t SET note = E'can\\'t';\nALTER TABLE app.t DROP COLUMN c;\nUPDATE app.t SET note = 'x';"),
    ).toEqual(["DROP COLUMN"]);
  });

  test("a column named type is no ALTER COLUMN … TYPE", () => {
    expect(
      rules("ALTER TABLE app.t ALTER COLUMN type SET DEFAULT 'a';\nALTER TABLE app.u ALTER COLUMN type DROP NOT NULL;"),
    ).toEqual([]);
  });

  test("a serial column has its default", () => {
    expect(rules("ALTER TABLE app.t ADD COLUMN n bigserial NOT NULL;")).toEqual([]);
  });

  test("CREATE TABLE IF NOT EXISTS and ADD COLUMN IF NOT EXISTS add nothing", () => {
    expect(
      rules(
        "CREATE TABLE IF NOT EXISTS app.invoices (id int);\nALTER TABLE app.invoices DROP COLUMN legacy;\n" +
          "ALTER TABLE app.t ADD COLUMN IF NOT EXISTS status text;\nALTER TABLE app.t ADD CONSTRAINT ck CHECK (status IN ('a'));",
      ),
    ).toEqual(["DROP COLUMN", "a new constraint on an existing table"]);
  });

  test("a DROP CONSTRAINT IF EXISTS does not make a CHECK a replacement", () => {
    expect(
      rules("ALTER TABLE app.t DROP CONSTRAINT IF EXISTS made_up, ADD CONSTRAINT made_up CHECK (amount > 0);"),
    ).toEqual([
      "warning: a dropped constraint, which an ON CONFLICT of the previous release may need",
      "a new constraint on an existing table",
    ]);
  });

  test("a marker in the Down section waives nothing", () => {
    const text = "-- +goose Up\nALTER TABLE app.t DROP COLUMN c;\n-- +goose Down\n-- vantigo:contract restoring c\n";
    expect(check([{ path: "m.sql", text }]).errors.map((f) => f.rule)).toEqual(["DROP COLUMN"]);
  });
});

describe("constraints on columns the range adds", () => {
  test("a CHECK or foreign key on added columns holds for old inserts", () => {
    expect(
      rules(
        "ALTER TABLE app.things ADD COLUMN kind varchar(10) NOT NULL DEFAULT 'a', ADD COLUMN o uuid,\n" +
          "  ADD CONSTRAINT ck_kind CHECK (kind IN ('a', 'b') AND cardinality(ARRAY[kind]::text[]) = 1),\n" +
          "  ADD CONSTRAINT fk_o FOREIGN KEY (o) REFERENCES app.other (id);",
      ),
    ).toEqual([]);
  });

  test("a column added by an earlier statement of the range counts", () => {
    expect(
      rules("ALTER TABLE app.things ADD COLUMN n int;\nALTER TABLE app.things ADD CONSTRAINT ck CHECK (n > 0);"),
    ).toEqual([]);
  });

  test("a CHECK that names an existing column fails", () => {
    expect(rules("ALTER TABLE app.things ADD COLUMN n int, ADD CONSTRAINT ck CHECK (n IS NULL OR old > 0);")).toEqual([
      "a new constraint on an existing table",
    ]);
  });

  test("a UNIQUE on an added column fails: every old insert gets the same DEFAULT", () => {
    expect(rules("ALTER TABLE app.things ADD COLUMN n int NOT NULL DEFAULT 0, ADD UNIQUE (n);")).toEqual([
      "a new constraint on an existing table",
    ]);
  });

  test("a CHECK replacing one the statement drops is a warning", () => {
    expect(
      rules(
        "ALTER TABLE app.things DROP CONSTRAINT ck_state, ADD CONSTRAINT ck_state CHECK (state IN ('a', 'b', 'c'));",
      ),
    ).toEqual(["warning: a replaced CHECK, which must only widen"]);
  });

  test("so is one whose DROP is an earlier statement", () => {
    expect(
      rules(
        "ALTER TABLE app.things DROP CONSTRAINT ck_state;\nALTER TABLE app.things ADD CONSTRAINT ck_state CHECK (state IN ('a'));",
      ),
    ).toEqual(["warning: a replaced CHECK, which must only widen"]);
  });

  test("a warning does not fail the check", () => {
    const text = up("ALTER TABLE app.things DROP CONSTRAINT ck, ADD CONSTRAINT ck CHECK (state IN ('a'));");
    const result = check([{ path: "m.sql", text }]);
    expect(result.errors).toEqual([]);
    expect(result.warnings.map((f) => f.rule)).toEqual(["a replaced CHECK, which must only widen"]);
  });
});

describe("tables the range creates", () => {
  test("anything goes on a table created in the same range", () => {
    const range = additions([{ path: "a.sql", text: up("CREATE TABLE app.fresh (id uuid);") }]);
    expect(range.tables).toEqual(new Set(["app.fresh"]));
    expect(
      rules(
        [
          "ALTER TABLE app.fresh ADD CONSTRAINT ck CHECK (true)",
          'ALTER TABLE "app"."fresh" ALTER COLUMN id SET NOT NULL',
          "ALTER TABLE app.fresh RENAME COLUMN id TO key",
          "CREATE UNIQUE INDEX ux ON app.fresh (id)",
          "CREATE TRIGGER t BEFORE INSERT ON app.fresh FOR EACH ROW EXECUTE FUNCTION app.f()",
          "DROP TABLE app.fresh",
        ].join(";\n"),
        ["app.fresh"],
      ),
    ).toEqual([]);
  });

  test("a DROP TABLE naming an existing table among new ones still fails", () => {
    expect(rules("DROP TABLE app.fresh, app.old;", ["app.fresh"])).toEqual(["DROP TABLE"]);
  });
});

describe("goose sections and blocks", () => {
  test("only the Up section counts", () => {
    expect(analyse({ path: "m.sql", text: up("CREATE TABLE app.a (id int);") }, none())).toEqual([]);
  });

  test("a DO block's body is read, with the file's line numbers", () => {
    const text = [
      "-- +goose Up",
      "-- +goose StatementBegin",
      "DO $$",
      "BEGIN",
      "    ALTER TABLE app.things DROP COLUMN legacy;",
      "END",
      "$$;",
      "-- +goose StatementEnd",
    ].join("\n");
    expect(analyse({ path: "m.sql", text }, none())).toEqual([
      {
        file: "m.sql",
        line: 5,
        rule: "DROP COLUMN",
        level: "error",
        statement: "ALTER TABLE app.things DROP COLUMN legacy",
      },
    ]);
  });

  test("a function in a statement block is not", () => {
    const text = up(
      "-- +goose StatementBegin\nCREATE FUNCTION app.f() RETURNS void LANGUAGE plpgsql AS $body$\nBEGIN\n  DROP TABLE app.x;\nEND\n$body$;\n-- +goose StatementEnd",
    );
    expect(analyse({ path: "m.sql", text }, none())).toEqual([]);
  });

  test("the line is the statement's first", () => {
    const text = "-- +goose Up\n-- a comment\n\nALTER TABLE app.things\n  DROP COLUMN a;";
    expect(analyse({ path: "m.sql", text }, none())[0].line).toBe(4);
  });
});

describe("the contract marker", () => {
  test("reads the reason", () => {
    expect(contractReason("-- +goose Up\n-- vantigo:contract drops legacy, unread since 2026.11\n")).toBe(
      "drops legacy, unread since 2026.11",
    );
    expect(contractReason("--vantigo:contract\n")).toBe("");
    expect(contractReason("-- not a contract\n")).toBeUndefined();
  });

  test("a marked file is waived with its reason", () => {
    const text = up("-- vantigo:contract the expand shipped in 2026.11\nALTER TABLE app.things DROP COLUMN legacy;");
    expect(check([{ path: "m.sql", text }])).toEqual({
      errors: [],
      warnings: [],
      waived: [{ file: "m.sql", reason: "the expand shipped in 2026.11" }],
      missingReason: [],
    });
  });

  test("a marker without a reason fails", () => {
    const text = up("-- vantigo:contract\nALTER TABLE app.things DROP COLUMN legacy;");
    expect(check([{ path: "m.sql", text }]).missingReason).toEqual(["m.sql"]);
  });

  test("an unmarked file fails, and a table created by another added file is new", () => {
    const result = check([
      { path: "a.sql", text: up("CREATE TABLE app.fresh (id int);") },
      { path: "b.sql", text: up("ALTER TABLE app.fresh ALTER COLUMN id SET NOT NULL;\nDROP TABLE app.old;") },
    ]);
    expect(result.errors.map((f) => [f.file, f.rule])).toEqual([["b.sql", "DROP TABLE"]]);
  });
});
