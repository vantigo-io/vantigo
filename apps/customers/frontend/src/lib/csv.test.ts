import { describe, expect, it } from "vitest";
import { csvCell, failedRowsCsv, parseCsv } from "./csv";

describe("parseCsv", () => {
  it("reads the export's form — BOM, semicolons, CRLF, quotes — and numbers rows as the server does", () => {
    const table = parseCsv('\ufeffname;phone\r\n"Fjord; ""Nord"" AS";\'+47 22\r\n;\r\n"to\r\nlinjer";1\r\n');
    expect(table.header).toEqual(["name", "phone"]);
    // The all-blank record takes no number, exactly as readCSVFile skips it.
    expect(table.records).toEqual([
      { row: 1, cells: ['Fjord; "Nord" AS', "'+47 22"] },
      { row: 2, cells: ["to\r\nlinjer", "1"] },
    ]);
  });

  it("skips an empty line before the header, as Go's encoding/csv does", () => {
    const table = parseCsv("\r\nname\r\nA\r\n");
    expect(table.header).toEqual(["name"]);
    expect(table.records).toEqual([{ row: 1, cells: ["A"] }]);
  });

  it("takes LF, no BOM, and skips empty lines", () => {
    expect(parseCsv("name\nA\n\nB").records).toEqual([
      { row: 1, cells: ["A"] },
      { row: 2, cells: ["B"] },
    ]);
  });
});

describe("csvCell", () => {
  it("guards a formula and quotes what would end the cell", () => {
    expect(csvCell("=SUM(A1)")).toBe("'=SUM(A1)");
    expect(csvCell("a;b")).toBe('"a;b"');
    expect(csvCell('say "hi"')).toBe('"say ""hi"""');
    expect(csvCell("'+47 22")).toBe("'+47 22");
    expect(csvCell("Fjord AS")).toBe("Fjord AS");
  });
});

describe("failedRowsCsv", () => {
  const table = parseCsv("\ufeffname;email\r\nOk AS;ok@x.no\r\nFeil AS;nei\r\nKort AS\r\n");

  it("keeps only the rows that failed, every cell as it was, behind an error column put first", () => {
    const csv = failedRowsCsv(table, [
      { row: 2, column: "email", message: "An email address must look like name@example.com, but was 'nei'" },
      { row: 3, column: null, message: "This row has 1 cells, but the header has 2" },
      { row: 2, column: "name", message: "Second problem" },
    ]);
    expect(csv).toBe(
      "\ufefferror;name;email\r\n" +
        "email: An email address must look like name@example.com, but was 'nei' | name: Second problem;Feil AS;nei\r\n" +
        // Short stays short: a blank cell here would be a write that clears email.
        "This row has 1 cells, but the header has 2;Kort AS\r\n",
    );
  });

  it("keeps every cell of a row longer than the header, so it stays exactly as much too long", () => {
    const long = parseCsv("name;email\r\nA;a@x.no;extra\r\n");
    expect(failedRowsCsv(long, [{ row: 1, column: null, message: "This row has 3 cells, but the header has 2" }])).toBe(
      "\ufefferror;name;email\r\nThis row has 3 cells, but the header has 2;A;a@x.no;extra\r\n",
    );
  });

  it("replaces the error column of a failed-rows file being re-run rather than adding a second", () => {
    const rerun = parseCsv("error;name\r\nold problem;Feil AS\r\nold short\r\n");
    expect(
      failedRowsCsv(rerun, [
        { row: 1, column: "name", message: "new problem" },
        { row: 2, column: null, message: "This row has 1 cells, but the header has 2" },
      ]),
    ).toBe("\ufefferror;name\r\nname: new problem;Feil AS\r\nThis row has 1 cells, but the header has 2\r\n");
  });

  it("moves an error column that is not first to the front, the old text of every row going with it", () => {
    const rerun = parseCsv("name;error;email\r\nFeil AS;old problem;nei\r\nLang AS;old;a@x.no;extra\r\nKort AS\r\n");
    expect(
      failedRowsCsv(rerun, [
        { row: 1, column: "email", message: "new problem" },
        { row: 2, column: null, message: "This row has 4 cells, but the header has 3" },
        { row: 3, column: null, message: "This row has 1 cells, but the header has 3" },
      ]),
    ).toBe(
      "\ufefferror;name;email\r\n" +
        "email: new problem;Feil AS;nei\r\n" +
        "This row has 4 cells, but the header has 3;Lang AS;a@x.no;extra\r\n" +
        // It never reached the old error column, so it has no old text to lose.
        "This row has 1 cells, but the header has 3;Kort AS\r\n",
    );
  });
});
